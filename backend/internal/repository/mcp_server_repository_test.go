package repository_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/testutil"
)

func TestMCPServerRepository_InsertGetListUpdateDelete(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewMCPServerRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	s := &domain.MCPServer{
		TenantID:           tenantID,
		Name:               "Threat Intel MCP",
		Transport:          "http",
		EndpointOrCommand:  "https://mcp.example.com",
		AllowedTools:       []string{"lookup_ip", "quarantine_host"},
		EnabledFor:         []string{"alert", "incident"},
		SideEffectingTools: []string{"quarantine_host"},
	}
	require.NoError(t, repo.Insert(t.Context(), tx, s))
	require.NotEqual(t, [16]byte{}, s.ID)
	assert.True(t, s.IsEnabled, "servers are enabled by default on insert")

	t.Run("get", func(t *testing.T) {
		got, err := repo.Get(t.Context(), tx, s.ID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "Threat Intel MCP", got.Name)
		assert.Equal(t, []string{"lookup_ip", "quarantine_host"}, got.AllowedTools)
		assert.Equal(t, []string{"quarantine_host"}, got.SideEffectingTools)
	})

	t.Run("get unknown id returns nil", func(t *testing.T) {
		got, err := repo.Get(t.Context(), tx, uuid.New())
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("list", func(t *testing.T) {
		list, err := repo.List(t.Context(), tx)
		require.NoError(t, err)
		require.Len(t, list, 1)
	})

	t.Run("update", func(t *testing.T) {
		s.Name = "Threat Intel MCP v2"
		s.AllowedTools = []string{"lookup_ip"}
		require.NoError(t, repo.Update(t.Context(), tx, s))

		got, err := repo.Get(t.Context(), tx, s.ID)
		require.NoError(t, err)
		assert.Equal(t, "Threat Intel MCP v2", got.Name)
		assert.Equal(t, []string{"lookup_ip"}, got.AllowedTools)
	})

	t.Run("set enabled", func(t *testing.T) {
		require.NoError(t, repo.SetEnabled(t.Context(), tx, s.ID, false))
		got, err := repo.Get(t.Context(), tx, s.ID)
		require.NoError(t, err)
		assert.False(t, got.IsEnabled)
	})

	t.Run("delete", func(t *testing.T) {
		require.NoError(t, repo.Delete(t.Context(), tx, s.ID))
		got, err := repo.Get(t.Context(), tx, s.ID)
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}

func strPtr(s string) *string { return &s }

func TestMCPServerRepository_AuthColumns(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewMCPServerRepository()

	base := func(name string) *domain.MCPServer {
		return &domain.MCPServer{
			TenantID: tenantID, Name: name, Transport: "http", EndpointOrCommand: "https://mcp.example.com",
			AllowedTools: []string{}, EnabledFor: []string{}, SideEffectingTools: []string{},
		}
	}

	t.Run("a zero AuthType is stored as none", func(t *testing.T) {
		tx := testutil.BeginTx(t, pool, tenantID)
		s := base("zero")
		require.NoError(t, repo.Insert(t.Context(), tx, s))
		got, err := repo.Get(t.Context(), tx, s.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.MCPAuthNone, got.AuthType)
	})

	t.Run("each auth type round-trips its own columns", func(t *testing.T) {
		tx := testutil.BeginTx(t, pool, tenantID)

		apiKey := base("api-key")
		apiKey.AuthType, apiKey.AuthHeaderName, apiKey.AuthSecretRef = domain.MCPAuthAPIKey, strPtr("X-Api-Key"), strPtr("ref-1")
		bearer := base("bearer")
		bearer.AuthType, bearer.AuthSecretRef = domain.MCPAuthBearer, strPtr("ref-2")
		oauth := base("oauth")
		oauth.AuthType, oauth.OAuthTokenURL, oauth.OAuthClientID, oauth.OAuthClientSecretRef =
			domain.MCPAuthOAuth, strPtr("https://auth.example.com/token"), strPtr("client"), strPtr("ref-3")

		for _, s := range []*domain.MCPServer{apiKey, bearer, oauth} {
			require.NoError(t, repo.Insert(t.Context(), tx, s))
		}

		got, err := repo.Get(t.Context(), tx, apiKey.ID)
		require.NoError(t, err)
		assert.Equal(t, "X-Api-Key", *got.AuthHeaderName)
		assert.Equal(t, "ref-1", *got.AuthSecretRef)
		assert.Nil(t, got.OAuthClientSecretRef)

		got, err = repo.Get(t.Context(), tx, oauth.ID)
		require.NoError(t, err)
		assert.Equal(t, "https://auth.example.com/token", *got.OAuthTokenURL)
		assert.Equal(t, "client", *got.OAuthClientID)
		assert.Equal(t, "ref-3", *got.OAuthClientSecretRef)
		assert.Nil(t, got.AuthSecretRef)

		// Switching away must clear the old columns, not leave them dangling.
		oauth.AuthType, oauth.OAuthTokenURL, oauth.OAuthClientID, oauth.OAuthClientSecretRef = domain.MCPAuthNone, nil, nil, nil
		require.NoError(t, repo.Update(t.Context(), tx, oauth))
		got, err = repo.Get(t.Context(), tx, oauth.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.MCPAuthNone, got.AuthType)
		assert.Nil(t, got.OAuthClientSecretRef)
	})

	// The shape CHECK is the backstop for any writer that skips the service's
	// validation. Each case runs in its own transaction since a failed insert
	// aborts it.
	incoherent := map[string]func(*domain.MCPServer){
		"api_key without a header": func(s *domain.MCPServer) { s.AuthType, s.AuthSecretRef = domain.MCPAuthAPIKey, strPtr("r") },
		"api_key without a secret": func(s *domain.MCPServer) { s.AuthType, s.AuthHeaderName = domain.MCPAuthAPIKey, strPtr("X-Key") },
		"bearer without a secret":  func(s *domain.MCPServer) { s.AuthType = domain.MCPAuthBearer },
		"bearer with a header": func(s *domain.MCPServer) {
			s.AuthType, s.AuthSecretRef, s.AuthHeaderName = domain.MCPAuthBearer, strPtr("r"), strPtr("X")
		},
		"none still holding a secret": func(s *domain.MCPServer) { s.AuthType, s.AuthSecretRef = domain.MCPAuthNone, strPtr("r") },
		"oauth missing the client secret": func(s *domain.MCPServer) {
			s.AuthType, s.OAuthTokenURL, s.OAuthClientID = domain.MCPAuthOAuth, strPtr("https://a/t"), strPtr("c")
		},
		"oauth with a stray bearer secret": func(s *domain.MCPServer) {
			s.AuthType, s.OAuthTokenURL, s.OAuthClientID, s.OAuthClientSecretRef, s.AuthSecretRef =
				domain.MCPAuthOAuth, strPtr("https://a/t"), strPtr("c"), strPtr("r"), strPtr("stray")
		},
		"unknown type": func(s *domain.MCPServer) { s.AuthType = "kerberos" },
	}
	for name, mutate := range incoherent {
		t.Run("CHECK rejects "+name, func(t *testing.T) {
			tx := testutil.BeginTx(t, pool, tenantID)
			s := base("bad-" + name)
			mutate(s)
			assert.Error(t, repo.Insert(t.Context(), tx, s))
		})
	}
}
