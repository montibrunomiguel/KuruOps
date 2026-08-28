package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/secrets"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/testutil"
)

// fakeSlackConfigRepo lets a test fail a specific repo call on demand --
// SlackConfigService takes an interface (not the concrete
// *repository.SlackConfigRepository) specifically so this is possible. See
// fakeStorageConfigRepo (storage_config_service_test.go) for the fuller
// version of this reasoning.
type fakeSlackConfigRepo struct {
	getErr    error
	deleteErr error
}

func (f *fakeSlackConfigRepo) Get(context.Context, pgx.Tx) (*domain.SlackConfig, error) {
	return nil, f.getErr
}
func (f *fakeSlackConfigRepo) Upsert(context.Context, pgx.Tx, *domain.SlackConfig) error {
	return nil
}
func (f *fakeSlackConfigRepo) Delete(context.Context, pgx.Tx) error { return f.deleteErr }

// newSlackConfigServiceWithOAuth wires a real OAuthStateService plus a
// fake Slack client ID/secret -- everything short of the actual token
// exchange with Slack, which has no unit-test coverage here for the same
// reason StorageConfigService's GDrive OAuth happy path doesn't either: no
// live credentials/network in this test run (see
// internal/slackclient/oauth_internal_test.go for where the exchange
// itself IS covered, via a redirected test server).
func newSlackConfigServiceWithOAuth(t *testing.T) *service.SlackConfigService {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	oauthStates := service.NewOAuthStateService(pool, repository.NewOAuthStateRepository())
	return service.NewSlackConfigService(pool, repository.NewSlackConfigRepository(), secrets.NewEnvStore(),
		oauthStates, "test-client-id", "test-client-secret", "https://kuruops.example/auth/oauth/slack/callback", repository.NewAdminAuditEventRepository())
}

func TestSlackConfigService_GetDisconnect(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	svc := service.NewSlackConfigService(pool, repository.NewSlackConfigRepository(), secrets.NewEnvStore(), nil, "", "", "", repository.NewAdminAuditEventRepository())

	t.Run("no workspace connected returns nil, not an error", func(t *testing.T) {
		cfg, err := svc.Get(t.Context(), tenantID)
		require.NoError(t, err)
		assert.Nil(t, cfg)
	})

	t.Run("disconnecting when nothing is connected is a no-op, not an error", func(t *testing.T) {
		require.NoError(t, svc.Disconnect(t.Context(), tenantID, actorID))
	})
}

func TestSlackConfigService_OAuth(t *testing.T) {
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "admin", nil)

	t.Run("GetAuthorizeURL refuses when Slack isn't configured for this deployment", func(t *testing.T) {
		pool := testutil.RequireTestDB(t)
		svc := service.NewSlackConfigService(pool, repository.NewSlackConfigRepository(), secrets.NewEnvStore(), nil, "", "", "", repository.NewAdminAuditEventRepository())
		_, err := svc.GetAuthorizeURL(t.Context(), tenantID, userID)
		assert.ErrorContains(t, err, "not configured")
	})

	t.Run("GetAuthorizeURL returns a real Slack consent URL carrying the state and bot scopes", func(t *testing.T) {
		svc := newSlackConfigServiceWithOAuth(t)
		url, err := svc.GetAuthorizeURL(t.Context(), tenantID, userID)
		require.NoError(t, err)
		assert.Contains(t, url, "slack.com/oauth/v2/authorize")
		assert.Contains(t, url, "state=")
		assert.Contains(t, url, "chat%3Awrite")
		assert.Contains(t, url, "client_id=test-client-id")
	})

	t.Run("HandleOAuthCallback rejects an invalid/expired state before ever touching Slack", func(t *testing.T) {
		svc := newSlackConfigServiceWithOAuth(t)
		err := svc.HandleOAuthCallback(t.Context(), tenantID, "some-code", "not-a-real-state")
		assert.ErrorContains(t, err, "invalid or expired oauth state")
	})
}

// TestSlackConfigService_RepoErrors exercises Disconnect's "load existing
// config to build the audit diff, then delete" error-wrapping branches --
// unreachable via a real Postgres integration test.
func TestSlackConfigService_RepoErrors(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)

	t.Run("Disconnect wraps a Get failure", func(t *testing.T) {
		svc := service.NewSlackConfigService(pool, &fakeSlackConfigRepo{getErr: errors.New("get boom")}, secrets.NewEnvStore(), nil, "", "", "", repository.NewAdminAuditEventRepository())
		err := svc.Disconnect(t.Context(), tenantID, actorID)
		assert.ErrorContains(t, err, "get boom")
	})

	t.Run("Disconnect wraps a Delete failure", func(t *testing.T) {
		svc := service.NewSlackConfigService(pool, &fakeSlackConfigRepo{deleteErr: errors.New("delete boom")}, secrets.NewEnvStore(), nil, "", "", "", repository.NewAdminAuditEventRepository())
		err := svc.Disconnect(t.Context(), tenantID, actorID)
		assert.ErrorContains(t, err, "delete boom")
	})
}
