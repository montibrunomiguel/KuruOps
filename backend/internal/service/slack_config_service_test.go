package service_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

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
		oauthStates, "test-client-id", "test-client-secret", "https://argusops.example/auth/oauth/slack/callback")
}

func TestSlackConfigService_GetDisconnect(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	svc := service.NewSlackConfigService(pool, repository.NewSlackConfigRepository(), secrets.NewEnvStore(), nil, "", "", "")

	t.Run("no workspace connected returns nil, not an error", func(t *testing.T) {
		cfg, err := svc.Get(t.Context(), tenantID)
		require.NoError(t, err)
		assert.Nil(t, cfg)
	})

	t.Run("disconnecting when nothing is connected is a no-op, not an error", func(t *testing.T) {
		require.NoError(t, svc.Disconnect(t.Context(), tenantID))
	})
}

func TestSlackConfigService_OAuth(t *testing.T) {
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "admin", nil)

	t.Run("GetAuthorizeURL refuses when Slack isn't configured for this deployment", func(t *testing.T) {
		pool := testutil.RequireTestDB(t)
		svc := service.NewSlackConfigService(pool, repository.NewSlackConfigRepository(), secrets.NewEnvStore(), nil, "", "", "")
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
