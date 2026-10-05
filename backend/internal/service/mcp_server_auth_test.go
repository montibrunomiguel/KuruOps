package service_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/secrets"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/testutil"
)

func newMCPAuthSvc(t *testing.T) (svc *service.MCPServerService, store *secrets.EnvStore, create func(name string, auth service.MCPServerAuthInput) (*domain.MCPServer, error), update func(srv *domain.MCPServer, mutate func(*service.MCPServerSaveInput)) (*domain.MCPServer, error), auditJSON func() string) {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	store = secrets.NewEnvStore()
	auditRepo := repository.NewAdminAuditEventRepository()
	svc = service.NewMCPServerService(pool, repository.NewMCPServerRepository(), store, auditRepo)

	create = func(name string, auth service.MCPServerAuthInput) (*domain.MCPServer, error) {
		return svc.Create(t.Context(), tenantID, actorID, service.MCPServerSaveInput{
			Name: name, Transport: "http", EndpointOrCommand: "https://mcp.example.com/mcp", Auth: auth,
		})
	}
	update = func(srv *domain.MCPServer, mutate func(*service.MCPServerSaveInput)) (*domain.MCPServer, error) {
		in := service.MCPServerSaveInput{
			Name: srv.Name, Transport: srv.Transport, EndpointOrCommand: srv.EndpointOrCommand,
			AllowedTools: srv.AllowedTools, EnabledFor: srv.EnabledFor, SideEffectingTools: srv.SideEffectingTools,
		}
		mutate(&in)
		return svc.Update(t.Context(), tenantID, actorID, srv.ID, in)
	}
	auditJSON = func() string {
		tx := testutil.BeginTx(t, pool, tenantID)
		events, err := auditRepo.List(t.Context(), tx, nil, 100)
		require.NoError(t, err)
		var all []byte
		for _, e := range events {
			all = append(all, e.Data...)
		}
		return string(all)
	}
	return
}

func resolve(t *testing.T, store *secrets.EnvStore, ref *string) string {
	t.Helper()
	require.NotNil(t, ref)
	v, err := store.Resolve(t.Context(), *ref)
	require.NoError(t, err)
	return v
}

func TestMCPServerService_Auth_CreateEachType(t *testing.T) {
	_, store, create, _, auditJSON := newMCPAuthSvc(t)

	t.Run("none stores no credential", func(t *testing.T) {
		srv, err := create("plain", service.MCPServerAuthInput{})
		require.NoError(t, err)
		assert.Equal(t, domain.MCPAuthNone, srv.AuthType)
		assert.Nil(t, srv.AuthSecretRef)
		assert.Nil(t, srv.OAuthClientSecretRef)
	})

	t.Run("api_key stores header and key", func(t *testing.T) {
		srv, err := create("keyed", service.MCPServerAuthInput{Type: domain.MCPAuthAPIKey, APIKeyHeader: " X-Api-Key ", APIKey: " key-123\n"})
		require.NoError(t, err)
		assert.Equal(t, domain.MCPAuthAPIKey, srv.AuthType)
		require.NotNil(t, srv.AuthHeaderName)
		assert.Equal(t, "X-Api-Key", *srv.AuthHeaderName, "header is trimmed")
		assert.Equal(t, "key-123", resolve(t, store, srv.AuthSecretRef), "the key is trimmed of the newline a paste brings along")
		assert.NotContains(t, *srv.AuthSecretRef, "key-123")
	})

	t.Run("bearer stores the token", func(t *testing.T) {
		srv, err := create("bearer", service.MCPServerAuthInput{Type: domain.MCPAuthBearer, BearerToken: "tok-abc"})
		require.NoError(t, err)
		assert.Equal(t, domain.MCPAuthBearer, srv.AuthType)
		assert.Equal(t, "tok-abc", resolve(t, store, srv.AuthSecretRef))
		assert.Nil(t, srv.AuthHeaderName)
	})

	t.Run("oauth stores the client secret, not the api-key/bearer slot", func(t *testing.T) {
		srv, err := create("oauthed", service.MCPServerAuthInput{
			Type: domain.MCPAuthOAuth, OAuthTokenURL: "https://auth.example.com/oauth/token",
			OAuthClientID: "client-1", OAuthClientSecret: "client-secret-xyz",
		})
		require.NoError(t, err)
		assert.Equal(t, domain.MCPAuthOAuth, srv.AuthType)
		assert.Nil(t, srv.AuthSecretRef)
		assert.Equal(t, "client-secret-xyz", resolve(t, store, srv.OAuthClientSecretRef))
		assert.Equal(t, "client-1", *srv.OAuthClientID)
		assert.Equal(t, "https://auth.example.com/oauth/token", *srv.OAuthTokenURL)
	})

	t.Run("no plaintext secret reaches the audit log or the JSON response", func(t *testing.T) {
		audit := auditJSON()
		for _, secret := range []string{"key-123", "tok-abc", "client-secret-xyz"} {
			assert.NotContains(t, audit, secret)
		}
		srv, err := create("json-check", service.MCPServerAuthInput{Type: domain.MCPAuthBearer, BearerToken: "tok-json"})
		require.NoError(t, err)
		body, err := json.Marshal(srv)
		require.NoError(t, err)
		assert.NotContains(t, string(body), "tok-json")
		assert.NotContains(t, string(body), "SecretRef")
		assert.Contains(t, string(body), `"authType":"bearer"`)
	})
}

func TestMCPServerService_Auth_CreateValidation(t *testing.T) {
	_, _, create, _, _ := newMCPAuthSvc(t)
	oauth := func(mutate func(*service.MCPServerAuthInput)) service.MCPServerAuthInput {
		in := service.MCPServerAuthInput{Type: domain.MCPAuthOAuth, OAuthTokenURL: "https://auth.example.com/token", OAuthClientID: "c", OAuthClientSecret: "s"}
		mutate(&in)
		return in
	}

	cases := map[string]struct {
		in   service.MCPServerAuthInput
		want string
	}{
		"unknown type":                 {service.MCPServerAuthInput{Type: "kerberos"}, "authType must be one of"},
		"secret without a type":        {service.MCPServerAuthInput{BearerToken: "x"}, "authType is required"},
		"none with a stray field":      {service.MCPServerAuthInput{Type: "none", APIKey: "x"}, "no authentication fields"},
		"api_key without header":       {service.MCPServerAuthInput{Type: "api_key", APIKey: "k"}, "apiKeyHeader"},
		"api_key without key":          {service.MCPServerAuthInput{Type: "api_key", APIKeyHeader: "X-Key"}, "apiKey is required"},
		"api_key reserved header":      {service.MCPServerAuthInput{Type: "api_key", APIKeyHeader: "Host", APIKey: "k"}, "reserved"},
		"api_key header injection":     {service.MCPServerAuthInput{Type: "api_key", APIKeyHeader: "X-Key\r\nX-Evil", APIKey: "k"}, "invalid character"},
		"api_key value injection":      {service.MCPServerAuthInput{Type: "api_key", APIKeyHeader: "X-Key", APIKey: "k\r\nX-Evil: 1"}, "control character"},
		"api_key with a bearer field":  {service.MCPServerAuthInput{Type: "api_key", APIKeyHeader: "X-Key", APIKey: "k", BearerToken: "t"}, "only apiKeyHeader and apiKey"},
		"bearer without token":         {service.MCPServerAuthInput{Type: "bearer"}, "bearerToken is required"},
		"bearer with control char":     {service.MCPServerAuthInput{Type: "bearer", BearerToken: "a\x00b"}, "control character"},
		"bearer with an api_key field": {service.MCPServerAuthInput{Type: "bearer", BearerToken: "t", APIKeyHeader: "X"}, "only bearerToken"},
		"oauth http token url":         {oauth(func(a *service.MCPServerAuthInput) { a.OAuthTokenURL = "http://auth.example.com/token" }), "must use https"},
		"oauth non-url":                {oauth(func(a *service.MCPServerAuthInput) { a.OAuthTokenURL = "not a url" }), "oauthTokenUrl"},
		"oauth ftp scheme":             {oauth(func(a *service.MCPServerAuthInput) { a.OAuthTokenURL = "ftp://auth.example.com/token" }), "https"},
		"oauth embedded credentials":   {oauth(func(a *service.MCPServerAuthInput) { a.OAuthTokenURL = "https://user:pw@auth.example.com/token" }), "embedded credentials"},
		"oauth fragment":               {oauth(func(a *service.MCPServerAuthInput) { a.OAuthTokenURL = "https://auth.example.com/token#x" }), "fragment"},
		"oauth missing token url":      {oauth(func(a *service.MCPServerAuthInput) { a.OAuthTokenURL = "" }), "oauthTokenUrl"},
		"oauth missing client id":      {oauth(func(a *service.MCPServerAuthInput) { a.OAuthClientID = "" }), "oauthClientId"},
		"oauth missing client secret":  {oauth(func(a *service.MCPServerAuthInput) { a.OAuthClientSecret = "" }), "oauthClientSecret is required"},
		"oauth with a bearer field":    {oauth(func(a *service.MCPServerAuthInput) { a.BearerToken = "t" }), "only oauthTokenUrl"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := create("srv-"+name, tc.in)
			require.Error(t, err)
			assert.ErrorContains(t, err, tc.want)
		})
	}

	t.Run("oauth accepts http for a loopback token url (local development)", func(t *testing.T) {
		_, err := create("loopback", oauth(func(a *service.MCPServerAuthInput) { a.OAuthTokenURL = "http://localhost:8080/token" }))
		assert.NoError(t, err)
	})
}

func TestMCPServerService_Auth_EndpointWithEmbeddedCredentialsRejected(t *testing.T) {
	_, _, create, update, _ := newMCPAuthSvc(t)
	srv, err := create("creds-in-url", service.MCPServerAuthInput{})
	require.NoError(t, err)

	_, err = update(srv, func(in *service.MCPServerSaveInput) {
		in.EndpointOrCommand = "https://admin:hunter2@mcp.example.com"
	})
	assert.ErrorContains(t, err, "embedded credentials")
}

func TestMCPServerService_Auth_UpdateKeepSemantics(t *testing.T) {
	_, store, create, update, auditJSON := newMCPAuthSvc(t)

	srv, err := create("keep", service.MCPServerAuthInput{Type: domain.MCPAuthBearer, BearerToken: "original"})
	require.NoError(t, err)
	originalRef := *srv.AuthSecretRef

	t.Run("omitting authType leaves the credential untouched (Discover Tools saves like this)", func(t *testing.T) {
		got, err := update(srv, func(in *service.MCPServerSaveInput) { in.AllowedTools = []string{"lookup_ip"} })
		require.NoError(t, err)
		assert.Equal(t, domain.MCPAuthBearer, got.AuthType)
		assert.Equal(t, originalRef, *got.AuthSecretRef)
		assert.Equal(t, "original", resolve(t, store, got.AuthSecretRef))
	})

	t.Run("same type with a blank secret keeps it", func(t *testing.T) {
		got, err := update(srv, func(in *service.MCPServerSaveInput) { in.Auth = service.MCPServerAuthInput{Type: domain.MCPAuthBearer} })
		require.NoError(t, err)
		assert.Equal(t, "original", resolve(t, store, got.AuthSecretRef))
	})

	t.Run("a new secret rotates it and the audit event says so without leaking it", func(t *testing.T) {
		got, err := update(srv, func(in *service.MCPServerSaveInput) {
			in.Auth = service.MCPServerAuthInput{Type: domain.MCPAuthBearer, BearerToken: "rotated-secret"}
		})
		require.NoError(t, err)
		assert.Equal(t, "rotated-secret", resolve(t, store, got.AuthSecretRef))
		audit := auditJSON()
		assert.Contains(t, audit, `"credentialRotated": true`)
		assert.NotContains(t, audit, "rotated-secret")
		assert.NotContains(t, audit, "original")
	})

	t.Run("a secret sent without authType is an error, not a silent drop", func(t *testing.T) {
		_, err := update(srv, func(in *service.MCPServerSaveInput) { in.Auth = service.MCPServerAuthInput{BearerToken: "x"} })
		assert.ErrorContains(t, err, "authType is required")
	})

	t.Run("switching to none clears every credential column", func(t *testing.T) {
		got, err := update(srv, func(in *service.MCPServerSaveInput) { in.Auth = service.MCPServerAuthInput{Type: domain.MCPAuthNone} })
		require.NoError(t, err)
		assert.Equal(t, domain.MCPAuthNone, got.AuthType)
		assert.Nil(t, got.AuthSecretRef)
	})

	t.Run("switching type requires the new credential: the old one is never reinterpreted", func(t *testing.T) {
		b, err := create("switch", service.MCPServerAuthInput{Type: domain.MCPAuthBearer, BearerToken: "tok"})
		require.NoError(t, err)

		_, err = update(b, func(in *service.MCPServerSaveInput) {
			in.Auth = service.MCPServerAuthInput{Type: domain.MCPAuthAPIKey, APIKeyHeader: "X-Key"}
		})
		assert.ErrorContains(t, err, "apiKey is required", "a bearer token must not silently become an API key")

		got, err := update(b, func(in *service.MCPServerSaveInput) {
			in.Auth = service.MCPServerAuthInput{Type: domain.MCPAuthAPIKey, APIKeyHeader: "X-Key", APIKey: "new-key"}
		})
		require.NoError(t, err)
		assert.Equal(t, domain.MCPAuthAPIKey, got.AuthType)
		assert.Equal(t, "new-key", resolve(t, store, got.AuthSecretRef))
	})
}

// A stored credential must only ever be delivered to the destination it was
// saved for -- otherwise an admin who can't read it could repoint the server
// at a host they control, leave the secret field blank, and have it sent there.
func TestMCPServerService_Auth_StoredCredentialIsBoundToItsDestination(t *testing.T) {
	_, store, create, update, _ := newMCPAuthSvc(t)

	t.Run("bearer: changing the endpoint without re-entering the token is refused", func(t *testing.T) {
		srv, err := create("bound-bearer", service.MCPServerAuthInput{Type: domain.MCPAuthBearer, BearerToken: "tok"})
		require.NoError(t, err)

		_, err = update(srv, func(in *service.MCPServerSaveInput) { in.EndpointOrCommand = "https://attacker.example.net/mcp" })
		assert.ErrorContains(t, err, "can't be reused with a different endpoint", "authType omitted")

		_, err = update(srv, func(in *service.MCPServerSaveInput) {
			in.EndpointOrCommand = "https://attacker.example.net/mcp"
			in.Auth = service.MCPServerAuthInput{Type: domain.MCPAuthBearer}
		})
		assert.ErrorContains(t, err, "bearerToken is required", "authType given but the secret left blank")

		got, err := update(srv, func(in *service.MCPServerSaveInput) {
			in.EndpointOrCommand = "https://new.example.com/mcp"
			in.Auth = service.MCPServerAuthInput{Type: domain.MCPAuthBearer, BearerToken: "fresh"}
		})
		require.NoError(t, err)
		assert.Equal(t, "fresh", resolve(t, store, got.AuthSecretRef))
	})

	t.Run("api_key: same rule, and changing only the header keeps the key", func(t *testing.T) {
		srv, err := create("bound-key", service.MCPServerAuthInput{Type: domain.MCPAuthAPIKey, APIKeyHeader: "X-Key", APIKey: "k"})
		require.NoError(t, err)

		_, err = update(srv, func(in *service.MCPServerSaveInput) {
			in.EndpointOrCommand = "https://attacker.example.net/mcp"
			in.Auth = service.MCPServerAuthInput{Type: domain.MCPAuthAPIKey, APIKeyHeader: "X-Key"}
		})
		assert.ErrorContains(t, err, "apiKey is required")

		got, err := update(srv, func(in *service.MCPServerSaveInput) {
			in.Auth = service.MCPServerAuthInput{Type: domain.MCPAuthAPIKey, APIKeyHeader: "X-Other-Header"}
		})
		require.NoError(t, err)
		assert.Equal(t, "X-Other-Header", *got.AuthHeaderName)
		assert.Equal(t, "k", resolve(t, store, got.AuthSecretRef))
	})

	t.Run("oauth: repointing the token URL or client id without the secret is refused", func(t *testing.T) {
		base := service.MCPServerAuthInput{
			Type: domain.MCPAuthOAuth, OAuthTokenURL: "https://auth.example.com/token", OAuthClientID: "c", OAuthClientSecret: "client-secret",
		}
		srv, err := create("bound-oauth", base)
		require.NoError(t, err)

		keep := service.MCPServerAuthInput{Type: domain.MCPAuthOAuth, OAuthTokenURL: "https://auth.example.com/token", OAuthClientID: "c"}
		got, err := update(srv, func(in *service.MCPServerSaveInput) { in.Auth = keep })
		require.NoError(t, err, "same url + client id: the secret is kept")
		assert.Equal(t, "client-secret", resolve(t, store, got.OAuthClientSecretRef))

		stolenURL := keep
		stolenURL.OAuthTokenURL = "https://attacker.example.net/token"
		_, err = update(srv, func(in *service.MCPServerSaveInput) { in.Auth = stolenURL })
		assert.ErrorContains(t, err, "oauthClientSecret is required")

		otherClient := keep
		otherClient.OAuthClientID = "someone-else"
		_, err = update(srv, func(in *service.MCPServerSaveInput) { in.Auth = otherClient })
		assert.ErrorContains(t, err, "oauthClientSecret is required")

		_, err = update(srv, func(in *service.MCPServerSaveInput) {
			in.EndpointOrCommand = "https://attacker.example.net/mcp"
			in.Auth = keep
		})
		assert.ErrorContains(t, err, "oauthClientSecret is required", "the minted access token would go to the new endpoint")
	})
}

// secrets.Store keys a secret by (tenant, purpose); a create that collides on
// name must not overwrite the existing server's credential on its way to
// failing the unique constraint.
func TestMCPServerService_Auth_DuplicateNameDoesNotClobberExistingSecret(t *testing.T) {
	_, store, create, update, _ := newMCPAuthSvc(t)

	first, err := create("dup", service.MCPServerAuthInput{Type: domain.MCPAuthBearer, BearerToken: "legit"})
	require.NoError(t, err)

	_, err = create("dup", service.MCPServerAuthInput{Type: domain.MCPAuthBearer, BearerToken: "attacker-overwrite"})
	assert.ErrorContains(t, err, "already exists")
	assert.Equal(t, "legit", resolve(t, store, first.AuthSecretRef))

	other, err := create("other", service.MCPServerAuthInput{Type: domain.MCPAuthBearer, BearerToken: "other-token"})
	require.NoError(t, err)
	_, err = update(other, func(in *service.MCPServerSaveInput) {
		in.Name = "dup"
		in.Auth = service.MCPServerAuthInput{Type: domain.MCPAuthBearer, BearerToken: "attacker-rename"}
	})
	assert.ErrorContains(t, err, "already exists")
	assert.Equal(t, "legit", resolve(t, store, first.AuthSecretRef), "renaming onto an existing name must not overwrite its secret either")
}
