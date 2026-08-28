package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/kuruops/kuruops/internal/db"
	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/secrets"
	"github.com/kuruops/kuruops/internal/slackclient"
)

// slackAuthorizeURL is Slack's own fixed OAuth v2 authorize endpoint, not a
// per-deployment setting.
const slackAuthorizeURL = "https://slack.com/oauth/v2/authorize"

// slackBotScopes must match docs/SLACK_APP_SETUP.md's manifest exactly --
// Slack's authorize screen only grants scopes the app's own manifest
// already declares, and changing this list requires reinstalling the app
// to the workspace (Slack forces re-consent on any scope change). Declared
// up front for the integration's whole eventual roadmap (channel/thread
// sync, not just this foundation phase) specifically to avoid repeat
// reinstall friction later -- see the plan's "declare everything now"
// rationale.
var slackBotScopes = []string{
	"chat:write", "channels:read", "channels:manage", "channels:history",
	"groups:read", "groups:write", "groups:history", "files:read", "im:write", "commands",
}

// slackConfigRepo is the subset of *repository.SlackConfigRepository this
// service calls -- an interface, not the concrete type, purely so tests can
// substitute a repo double that fails on demand to exercise the
// error-wrapping branches (a DB call failing mid-transaction) a real
// Postgres integration test can't trigger. *repository.SlackConfigRepository
// already satisfies this implicitly, so every existing constructor call
// site is unaffected.
type slackConfigRepo interface {
	Get(ctx context.Context, tx pgx.Tx) (*domain.SlackConfig, error)
	Upsert(ctx context.Context, tx pgx.Tx, c *domain.SlackConfig) error
	Delete(ctx context.Context, tx pgx.Tx) error
}

// SlackConfigService is Settings -> Conectores -> Slack: connect/disconnect
// a Slack workspace via bot-token OAuth. This is the foundation phase only
// -- Get(tenantID) returning non-nil is the gate every future Slack feature
// (thread sync, incident channel linking, ...) checks before offering its
// UI; nothing here yet calls the Slack Web API beyond the OAuth exchange
// itself (see internal/slackclient's doc comment for what's deliberately
// not built yet).
type SlackConfigService struct {
	pool    *db.Pool
	repo    slackConfigRepo
	secrets secrets.Store

	// oauthStates, clientID/clientSecret, and redirectURL back the
	// "Conectar ao Slack" flow -- clientID empty disables it entirely
	// (GetAuthorizeURL refuses with a clear error), same convention as
	// StorageConfigService's Google OAuth fields.
	oauthStates  *OAuthStateService
	clientID     string
	clientSecret string
	redirectURL  string

	audit *repository.AdminAuditEventRepository
}

func NewSlackConfigService(pool *db.Pool, repo slackConfigRepo, store secrets.Store, oauthStates *OAuthStateService, clientID, clientSecret, redirectURL string, audit *repository.AdminAuditEventRepository) *SlackConfigService {
	return &SlackConfigService{
		pool: pool, repo: repo, secrets: store,
		oauthStates: oauthStates, clientID: clientID, clientSecret: clientSecret, redirectURL: redirectURL,
		audit: audit,
	}
}

func slackConfigAuditFields(c *domain.SlackConfig) map[string]any {
	if c == nil {
		return nil
	}
	return map[string]any{
		"teamId": c.TeamID, "teamName": c.TeamName, "botUserId": c.BotUserID,
		"installedByUserId": c.InstalledByUserID, "grantedScopes": c.GrantedScopes,
		"botTokenSet": c.BotTokenSecretRef != "",
	}
}

// Get returns nil (not an error) when no workspace is connected -- this is
// the in-process check future Slack features call directly, no HTTP round
// trip needed.
func (s *SlackConfigService) Get(ctx context.Context, tenantID uuid.UUID) (*domain.SlackConfig, error) {
	var cfg *domain.SlackConfig
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		c, err := s.repo.Get(ctx, tx)
		cfg = c
		return err
	})
	return cfg, err
}

func (s *SlackConfigService) Disconnect(ctx context.Context, tenantID, actorID uuid.UUID) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		existing, err := s.repo.Get(ctx, tx)
		if err != nil {
			return err
		}
		if err := s.repo.Delete(ctx, tx); err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]any{"from": slackConfigAuditFields(existing), "to": nil})
		return s.audit.InsertEvent(ctx, tx, &domain.AdminAuditEvent{
			TenantID: tenantID, Area: "slack-config", Action: "disconnect", ActorType: domain.ActorUser, ActorID: actorID, Data: data,
		})
	})
}

// GetAuthorizeURL builds Slack's consent-screen URL for tenantID/userID to
// connect a workspace, carrying CSRF state via the shared OAuthStateService
// (no extra metadata needed -- unlike Google Drive's folder ID, there's
// nothing Slack-specific to survive the redirect round trip in this
// foundation phase).
func (s *SlackConfigService) GetAuthorizeURL(ctx context.Context, tenantID, userID uuid.UUID) (string, error) {
	if s.clientID == "" {
		return "", fmt.Errorf("Slack is not configured for this deployment -- set SLACK_CLIENT_ID/SLACK_CLIENT_SECRET (see docs/SLACK_APP_SETUP.md)")
	}
	state, err := s.oauthStates.Generate(ctx, tenantID, userID, domain.OAuthProviderSlack, nil)
	if err != nil {
		return "", err
	}
	q := url.Values{
		"client_id":    {s.clientID},
		"scope":        {strings.Join(slackBotScopes, ",")},
		"redirect_uri": {s.redirectURL},
		"state":        {state},
	}
	return slackAuthorizeURL + "?" + q.Encode(), nil
}

// HandleOAuthCallback consumes the state Slack's callback echoed back,
// exchanges code for a bot token, stores it via secrets.Store, and saves
// the workspace as connected -- the installing admin comes from the state
// row's UserID, since the callback request itself carries no session (see
// OAuthCallbackHandlers' doc comment). Returns an error, not an HTTP
// response -- same reasoning as StorageConfigService.HandleGDriveOAuthCallback:
// the caller turns this into a redirect, not a JSON body.
func (s *SlackConfigService) HandleOAuthCallback(ctx context.Context, tenantID uuid.UUID, code, state string) error {
	oauthState, err := s.oauthStates.Consume(ctx, tenantID, domain.OAuthProviderSlack, state)
	if err != nil {
		return err
	}
	if oauthState == nil {
		return fmt.Errorf("invalid or expired oauth state")
	}

	result, err := slackclient.ExchangeCode(ctx, s.clientID, s.clientSecret, code, s.redirectURL)
	if err != nil {
		return err
	}

	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		before, err := s.repo.Get(ctx, tx)
		if err != nil {
			return fmt.Errorf("load existing slack config: %w", err)
		}
		// Unlike GCS/S3's "blank means keep existing" secrets, Slack's
		// token exchange always returns a fresh bot token on every
		// successful callback -- nothing to fall back to, and nothing that
		// should fall back (reconnecting is expected to replace it).
		ref, err := s.secrets.Put(ctx, tenantID.String(), "slack-bot-token", result.AccessToken)
		if err != nil {
			return fmt.Errorf("store slack bot token: %w", err)
		}
		cfg := &domain.SlackConfig{
			TenantID: tenantID, BotTokenSecretRef: ref,
			TeamID: result.TeamID, TeamName: result.TeamName, BotUserID: result.BotUserID,
			InstalledByUserID: oauthState.UserID, GrantedScopes: result.Scope,
		}
		if err := s.repo.Upsert(ctx, tx, cfg); err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]any{"from": slackConfigAuditFields(before), "to": slackConfigAuditFields(cfg)})
		return s.audit.InsertEvent(ctx, tx, &domain.AdminAuditEvent{
			TenantID: tenantID, Area: "slack-config", Action: "connect", ActorType: domain.ActorUser, ActorID: oauthState.UserID, Data: data,
		})
	})
}
