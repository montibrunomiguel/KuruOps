package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"

	"github.com/kuruops/kuruops/internal/blobstore"
	"github.com/kuruops/kuruops/internal/db"
	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/secrets"
)

// storageConfigRepo is the subset of *repository.StorageConfigRepository
// this service calls -- an interface (rather than the concrete type
// directly) purely so tests can substitute a repo double that fails on
// demand, to exercise the "the DB call right before an audit-diff read
// errors out" branches that a real Postgres integration test has no way to
// trigger. *repository.StorageConfigRepository already satisfies this
// implicitly, so every existing constructor call site is unaffected.
type storageConfigRepo interface {
	Get(ctx context.Context, tx pgx.Tx) (*domain.StorageConfig, error)
	Upsert(ctx context.Context, tx pgx.Tx, c *domain.StorageConfig) error
	Delete(ctx context.Context, tx pgx.Tx) error
}

// StorageConfigService is Settings -> Storage Integration: lets an admin
// point alert/incident evidence uploads at an S3 or GCS bucket instead of
// the API container's local disk (see handlers.UploadHandlers). Mirrors
// IdentityConfigService's LDAP/SAML shape -- same "empty string on save
// means keep the existing secret" convention.
type StorageConfigService struct {
	pool      *db.Pool
	repo      storageConfigRepo
	secrets   secrets.Store
	uploadDir string
	audit     *repository.AdminAuditEventRepository

	// oauthStates, googleOAuthClientID/Secret, and googleOAuthRedirectURL
	// back the Google Drive "Connect your Google account" path only --
	// googleOAuthClientID empty disables that path entirely
	// (GetGDriveAuthorizeURL refuses with a clear error), leaving the
	// service-account path unaffected. See OAuthStateService's doc comment
	// for why the CSRF state mechanism is shared while everything else
	// about the OAuth exchange itself is not.
	oauthStates             *OAuthStateService
	googleOAuthClientID     string
	googleOAuthClientSecret string
	googleOAuthRedirectURL  string
}

func NewStorageConfigService(pool *db.Pool, repo storageConfigRepo, store secrets.Store, uploadDir string, oauthStates *OAuthStateService, googleOAuthClientID, googleOAuthClientSecret, googleOAuthRedirectURL string, audit *repository.AdminAuditEventRepository) *StorageConfigService {
	return &StorageConfigService{
		pool: pool, repo: repo, secrets: store, uploadDir: uploadDir,
		oauthStates:         oauthStates,
		googleOAuthClientID: googleOAuthClientID, googleOAuthClientSecret: googleOAuthClientSecret,
		googleOAuthRedirectURL: googleOAuthRedirectURL,
		audit:                  audit,
	}
}

// storageConfigAuditFields is the subset of domain.StorageConfig safe to put
// in an admin audit event's data column -- every *SecretRef field is opaque
// (never the plaintext credential) but still left out, same reasoning as
// llmProviderAuditFields/mcpServerAuditFields: meaningless to a human reader.
// A "credentialSet" boolean signals whether one is configured instead.
func storageConfigAuditFields(c *domain.StorageConfig) map[string]any {
	if c == nil {
		return nil
	}
	fields := map[string]any{"provider": c.Provider}
	switch c.Provider {
	case domain.StorageProviderS3:
		fields["s3Bucket"] = c.S3Bucket
		fields["s3Region"] = c.S3Region
		fields["s3AccessKeyId"] = c.S3AccessKeyID
		fields["s3SecretAccessKeySet"] = c.S3SecretAccessKeySecretRef != ""
	case domain.StorageProviderGCS:
		fields["gcsBucket"] = c.GCSBucket
		fields["gcsProjectId"] = c.GCSProjectID
		fields["gcsCredentialsSet"] = c.GCSCredentialsJSONSecretRef != ""
	case domain.StorageProviderGDrive:
		fields["gdriveFolderId"] = c.GDriveFolderID
		fields["gdriveAuthMethod"] = c.GDriveAuthMethod
		fields["gdriveServiceAccountCredentialsSet"] = c.GDriveServiceAccountJSONSecretRef != ""
		fields["gdriveOAuthConnectedEmail"] = c.GDriveOAuthConnectedEmail
	}
	return fields
}

func (s *StorageConfigService) Get(ctx context.Context, tenantID uuid.UUID) (*domain.StorageConfig, error) {
	var cfg *domain.StorageConfig
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		c, err := s.repo.Get(ctx, tx)
		cfg = c
		return err
	})
	return cfg, err
}

func (s *StorageConfigService) Delete(ctx context.Context, tenantID, actorID uuid.UUID) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		existing, err := s.repo.Get(ctx, tx)
		if err != nil {
			return err
		}
		if err := s.repo.Delete(ctx, tx); err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]any{"from": storageConfigAuditFields(existing), "to": nil})
		return s.audit.InsertEvent(ctx, tx, &domain.AdminAuditEvent{
			TenantID: tenantID, Area: "storage-config", Action: "delete", ActorType: domain.ActorUser, ActorID: actorID, Data: data,
		})
	})
}

type SaveS3Input struct {
	Bucket          string
	Region          string
	AccessKeyID     string
	SecretAccessKey string // plaintext; "" on update means keep existing
}

func (s *StorageConfigService) SaveS3(ctx context.Context, tenantID, actorID uuid.UUID, in SaveS3Input) error {
	if in.Bucket == "" || in.Region == "" || in.AccessKeyID == "" {
		return fmt.Errorf("bucket, region, and accessKeyId are required")
	}
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		before, err := s.repo.Get(ctx, tx)
		if err != nil {
			return fmt.Errorf("load existing storage config: %w", err)
		}
		ref, err := s.resolveSecretRef(ctx, tx, tenantID, in.SecretAccessKey, "storage-s3-secret-access-key", func(existing *domain.StorageConfig) string {
			if existing != nil && existing.Provider == domain.StorageProviderS3 {
				return existing.S3SecretAccessKeySecretRef
			}
			return ""
		})
		if err != nil {
			return err
		}
		cfg := &domain.StorageConfig{
			TenantID: tenantID, Provider: domain.StorageProviderS3,
			S3Bucket: &in.Bucket, S3Region: &in.Region, S3AccessKeyID: &in.AccessKeyID,
			S3SecretAccessKeySecretRef: ref,
		}
		if err := s.repo.Upsert(ctx, tx, cfg); err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]any{"from": storageConfigAuditFields(before), "to": storageConfigAuditFields(cfg)})
		return s.audit.InsertEvent(ctx, tx, &domain.AdminAuditEvent{
			TenantID: tenantID, Area: "storage-config", Action: "save-s3", ActorType: domain.ActorUser, ActorID: actorID, Data: data,
		})
	})
}

type SaveGCSInput struct {
	Bucket          string
	ProjectID       string
	CredentialsJSON string // plaintext service account key JSON; "" on update means keep existing
}

func (s *StorageConfigService) SaveGCS(ctx context.Context, tenantID, actorID uuid.UUID, in SaveGCSInput) error {
	if in.Bucket == "" || in.ProjectID == "" {
		return fmt.Errorf("bucket and projectId are required")
	}
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		before, err := s.repo.Get(ctx, tx)
		if err != nil {
			return fmt.Errorf("load existing storage config: %w", err)
		}
		ref, err := s.resolveSecretRef(ctx, tx, tenantID, in.CredentialsJSON, "storage-gcs-credentials-json", func(existing *domain.StorageConfig) string {
			if existing != nil && existing.Provider == domain.StorageProviderGCS {
				return existing.GCSCredentialsJSONSecretRef
			}
			return ""
		})
		if err != nil {
			return err
		}
		cfg := &domain.StorageConfig{
			TenantID: tenantID, Provider: domain.StorageProviderGCS,
			GCSBucket: &in.Bucket, GCSProjectID: &in.ProjectID,
			GCSCredentialsJSONSecretRef: ref,
		}
		if err := s.repo.Upsert(ctx, tx, cfg); err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]any{"from": storageConfigAuditFields(before), "to": storageConfigAuditFields(cfg)})
		return s.audit.InsertEvent(ctx, tx, &domain.AdminAuditEvent{
			TenantID: tenantID, Area: "storage-config", Action: "save-gcs", ActorType: domain.ActorUser, ActorID: actorID, Data: data,
		})
	})
}

type SaveGDriveServiceAccountInput struct {
	FolderID           string
	ServiceAccountJSON string // plaintext service account key JSON; "" on update means keep existing
}

// SaveGDriveServiceAccount is SaveGCS's Drive counterpart -- the service
// account named in ServiceAccountJSON must already be shared (from the
// Drive side) with access to FolderID; KuruOps has no way to grant that
// on the admin's behalf.
func (s *StorageConfigService) SaveGDriveServiceAccount(ctx context.Context, tenantID, actorID uuid.UUID, in SaveGDriveServiceAccountInput) error {
	if in.FolderID == "" {
		return fmt.Errorf("folderId is required")
	}
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		before, err := s.repo.Get(ctx, tx)
		if err != nil {
			return fmt.Errorf("load existing storage config: %w", err)
		}
		ref, err := s.resolveSecretRef(ctx, tx, tenantID, in.ServiceAccountJSON, "storage-gdrive-service-account-json", func(existing *domain.StorageConfig) string {
			if existing != nil && existing.Provider == domain.StorageProviderGDrive &&
				existing.GDriveAuthMethod != nil && *existing.GDriveAuthMethod == domain.GDriveAuthMethodServiceAccount {
				return existing.GDriveServiceAccountJSONSecretRef
			}
			return ""
		})
		if err != nil {
			return err
		}
		authMethod := domain.GDriveAuthMethodServiceAccount
		cfg := &domain.StorageConfig{
			TenantID: tenantID, Provider: domain.StorageProviderGDrive,
			GDriveFolderID: &in.FolderID, GDriveAuthMethod: &authMethod,
			GDriveServiceAccountJSONSecretRef: ref,
		}
		if err := s.repo.Upsert(ctx, tx, cfg); err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]any{"from": storageConfigAuditFields(before), "to": storageConfigAuditFields(cfg)})
		return s.audit.InsertEvent(ctx, tx, &domain.AdminAuditEvent{
			TenantID: tenantID, Area: "storage-config", Action: "save-gdrive-service-account", ActorType: domain.ActorUser, ActorID: actorID, Data: data,
		})
	})
}

// googleDriveOAuthConfig is the app-level Google OAuth client every
// tenant's "Connect your Google account" flow authenticates through --
// the broad drive scope (not the narrower drive.file) is required because
// the admin names an existing folder by ID rather than picking it through
// Google's file Picker widget, which is the only flow drive.file's
// narrower grant supports.
func (s *StorageConfigService) googleDriveOAuthConfig() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     s.googleOAuthClientID,
		ClientSecret: s.googleOAuthClientSecret,
		Endpoint:     google.Endpoint,
		RedirectURL:  s.googleOAuthRedirectURL,
		Scopes:       []string{drive.DriveScope, "openid", "email"},
	}
}

// GetGDriveAuthorizeURL builds the Google consent-screen URL for tenantID/
// userID to connect a Google account, carrying folderID through the
// redirect round trip via the shared OAuthStateService. Returns an error
// if this deployment has no Google OAuth client configured (see
// config.Config.GoogleOAuthClientID) -- the service-account path is
// unaffected either way.
func (s *StorageConfigService) GetGDriveAuthorizeURL(ctx context.Context, tenantID, userID uuid.UUID, folderID string) (string, error) {
	if s.googleOAuthClientID == "" {
		return "", fmt.Errorf("Google OAuth is not configured for this deployment -- set GOOGLE_OAUTH_CLIENT_ID/GOOGLE_OAUTH_CLIENT_SECRET, or use the service-account option instead")
	}
	if folderID == "" {
		return "", fmt.Errorf("folderId is required")
	}
	state, err := s.oauthStates.Generate(ctx, tenantID, userID, domain.OAuthProviderGDrive, map[string]string{"folderId": folderID})
	if err != nil {
		return "", err
	}
	// AccessTypeOffline + the explicit "prompt=consent" param: Google only
	// issues a refresh token on a user's very first consent unless
	// re-consent is forced, so reconnecting an already-authorized account
	// (e.g. after Disconnect) would otherwise come back with no refresh
	// token at all.
	url := s.googleDriveOAuthConfig().AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "consent"))
	return url, nil
}

// HandleGDriveOAuthCallback consumes the state Google's callback echoed
// back, exchanges code for a refresh token, resolves the connected
// account's email (for display only), stores the refresh token via
// secrets.Store, and saves the config. Returns an error state -- callers
// (the callback handler) turn that into a redirect back to the frontend
// with an error query param, not an HTTP error response, since this is a
// top-level browser navigation with no XHR caller to receive one.
func (s *StorageConfigService) HandleGDriveOAuthCallback(ctx context.Context, tenantID uuid.UUID, code, state string) error {
	oauthState, err := s.oauthStates.Consume(ctx, tenantID, domain.OAuthProviderGDrive, state)
	if err != nil {
		return err
	}
	if oauthState == nil {
		return fmt.Errorf("invalid or expired oauth state")
	}
	folderID := oauthState.Metadata["folderId"]
	if folderID == "" {
		return fmt.Errorf("oauth state is missing its folder id")
	}

	// Bound every outbound Google call in this flow. oauth2 reads its HTTP
	// client off the context, so this one value covers both Exchange below
	// and the cfg.Client(...) used for the userinfo lookup -- neither has a
	// timeout otherwise (oauth2 falls back to http.DefaultClient), and
	// /auth/oauth/* is mounted outside the api group's chimw.Timeout (see
	// router.go), so the request context carries no deadline to inherit
	// either. No httpguard: these are Google's own fixed endpoints, not
	// tenant-supplied URLs.
	ctx = context.WithValue(ctx, oauth2.HTTPClient, &http.Client{Timeout: 15 * time.Second})

	cfg := s.googleDriveOAuthConfig()
	token, err := cfg.Exchange(ctx, code)
	if err != nil {
		return fmt.Errorf("exchange oauth code: %w", err)
	}
	if token.RefreshToken == "" {
		return fmt.Errorf("Google did not return a refresh token -- try disconnecting and reconnecting")
	}

	email, err := fetchGoogleAccountEmail(ctx, cfg.Client(ctx, token))
	if err != nil {
		return fmt.Errorf("resolve connected google account: %w", err)
	}

	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		before, err := s.repo.Get(ctx, tx)
		if err != nil {
			return fmt.Errorf("load existing storage config: %w", err)
		}
		ref, err := secrets.PutOrKeepExisting(ctx, s.secrets, tenantID.String(), "storage-gdrive-oauth-refresh-token", token.RefreshToken, "")
		if err != nil {
			return err
		}
		authMethod := domain.GDriveAuthMethodOAuth
		cfg := &domain.StorageConfig{
			TenantID: tenantID, Provider: domain.StorageProviderGDrive,
			GDriveFolderID: &folderID, GDriveAuthMethod: &authMethod,
			GDriveOAuthRefreshTokenSecretRef: ref,
			GDriveOAuthConnectedEmail:        &email,
		}
		if err := s.repo.Upsert(ctx, tx, cfg); err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]any{"from": storageConfigAuditFields(before), "to": storageConfigAuditFields(cfg)})
		return s.audit.InsertEvent(ctx, tx, &domain.AdminAuditEvent{
			TenantID: tenantID, Area: "storage-config", Action: "save-gdrive-oauth", ActorType: domain.ActorUser, ActorID: oauthState.UserID, Data: data,
		})
	})
}

// fetchGoogleAccountEmail makes one authenticated GET against Google's
// userinfo endpoint to resolve which account is now connected, purely for
// display (Settings -> Storage Integration shows "Connected as
// <email>") -- a small direct HTTP call rather than pulling in a whole
// extra Google API client subpackage for one field.
func fetchGoogleAccountEmail(ctx context.Context, client *http.Client) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.googleapis.com/oauth2/v2/userinfo", nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("userinfo request failed: %s: %s", resp.Status, body)
	}
	var payload struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode userinfo response: %w", err)
	}
	return payload.Email, nil
}

// resolveSecretRef is SaveS3/SaveGCS's use of secrets.PutOrKeepExisting: a
// new value is always stored fresh; a blank one falls back to whatever the
// same provider already had. Unlike SMTP, the very first save for a
// provider requires a real value -- there's nothing to fall back to yet,
// and an empty storage credential isn't a valid configuration the way an
// unauthenticated SMTP relay is.
func (s *StorageConfigService) resolveSecretRef(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, plaintext, purpose string, existingRef func(*domain.StorageConfig) string) (string, error) {
	existing, err := s.repo.Get(ctx, tx)
	if err != nil {
		return "", fmt.Errorf("load existing storage config: %w", err)
	}
	ref, err := secrets.PutOrKeepExisting(ctx, s.secrets, tenantID.String(), purpose, plaintext, existingRef(existing))
	if err != nil {
		return "", err
	}
	if ref == "" {
		return "", fmt.Errorf("a credential value is required for initial configuration")
	}
	return ref, nil
}

// BuildStore resolves the blobstore.Store a caller should upload evidence
// to right now: the tenant's configured S3/GCS bucket if one exists, local
// disk otherwise. Called per-upload rather than cached, since an admin
// changing the integration should take effect on the very next upload
// without an API restart.
func (s *StorageConfigService) BuildStore(ctx context.Context, tenantID uuid.UUID) (blobstore.Store, error) {
	cfg, err := s.Get(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("load storage config: %w", err)
	}
	if cfg == nil {
		return blobstore.NewLocalStore(s.uploadDir), nil
	}

	switch cfg.Provider {
	case domain.StorageProviderS3:
		secretKey, err := s.secrets.Resolve(ctx, cfg.S3SecretAccessKeySecretRef)
		if err != nil {
			return nil, fmt.Errorf("resolve s3 secret: %w", err)
		}
		return blobstore.NewS3Store(*cfg.S3Region, *cfg.S3AccessKeyID, secretKey, *cfg.S3Bucket), nil
	case domain.StorageProviderGCS:
		credentialsJSON, err := s.secrets.Resolve(ctx, cfg.GCSCredentialsJSONSecretRef)
		if err != nil {
			return nil, fmt.Errorf("resolve gcs credentials: %w", err)
		}
		store, err := blobstore.NewGCSStore(ctx, credentialsJSON, *cfg.GCSBucket)
		if err != nil {
			return nil, fmt.Errorf("build gcs client: %w", err)
		}
		return store, nil
	case domain.StorageProviderGDrive:
		return s.buildGDriveStore(ctx, cfg)
	default:
		return blobstore.NewLocalStore(s.uploadDir), nil
	}
}

// buildGDriveStore picks the matching blobstore.GDriveStore constructor
// for cfg.GDriveAuthMethod -- see SaveGDriveServiceAccount/
// HandleGDriveOAuthCallback for how each secret ref gets populated.
func (s *StorageConfigService) buildGDriveStore(ctx context.Context, cfg *domain.StorageConfig) (blobstore.Store, error) {
	if cfg.GDriveAuthMethod == nil || cfg.GDriveFolderID == nil {
		return nil, fmt.Errorf("google drive storage config is incomplete")
	}
	switch *cfg.GDriveAuthMethod {
	case domain.GDriveAuthMethodServiceAccount:
		credentialsJSON, err := s.secrets.Resolve(ctx, cfg.GDriveServiceAccountJSONSecretRef)
		if err != nil {
			return nil, fmt.Errorf("resolve gdrive service account credentials: %w", err)
		}
		return blobstore.NewGDriveStoreFromServiceAccount(ctx, credentialsJSON, *cfg.GDriveFolderID)
	case domain.GDriveAuthMethodOAuth:
		refreshToken, err := s.secrets.Resolve(ctx, cfg.GDriveOAuthRefreshTokenSecretRef)
		if err != nil {
			return nil, fmt.Errorf("resolve gdrive oauth refresh token: %w", err)
		}
		return blobstore.NewGDriveStoreFromOAuth(ctx, s.googleOAuthClientID, s.googleOAuthClientSecret, refreshToken, *cfg.GDriveFolderID)
	default:
		return nil, fmt.Errorf("unknown gdrive auth method %q", *cfg.GDriveAuthMethod)
	}
}
