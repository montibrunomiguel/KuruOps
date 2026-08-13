package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/authn"
	"github.com/argusops/argusops/internal/httpserver/handlers"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func TestAccountHandlers_ChangePassword(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "analyst", nil)
	priv, err := authn.GenerateEphemeralKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	authSvc := service.NewAuthService(pool, repository.NewTenantRepository(), repository.NewUserRepository(), repository.NewRefreshTokenRepository(), service.NewRoleService(pool, repository.NewRoleRepository()), authn.NewIssuer(priv))
	apiTokenSvc := service.NewUserAPITokenService(pool, repository.NewUserAPITokenRepository(), repository.NewUserRepository())
	h := handlers.NewAccountHandlers(authSvc, apiTokenSvc)
	r := newRouter(h.Routes)

	t.Run("wrong current password -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"currentPassword": "wrong", "newPassword": "NewPassword123!"})
		req := withClaims(httptest.NewRequest("POST", "/change-password", bytes.NewReader(body)), tenantID, userID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("invalid JSON body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/change-password", bytes.NewReader([]byte("{not-json"))), tenantID, userID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("valid change -- 200 with a fresh token", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"currentPassword": testutil.TestPassword, "newPassword": "NewPassword123!"})
		req := withClaims(httptest.NewRequest("POST", "/change-password", bytes.NewReader(body)), tenantID, userID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)

		var resp map[string]string
		assert.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.NotEmpty(t, resp["token"])
	})
}

func TestAccountHandlers_UpdateProfile(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "analyst", nil)
	priv, err := authn.GenerateEphemeralKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	authSvc := service.NewAuthService(pool, repository.NewTenantRepository(), repository.NewUserRepository(), repository.NewRefreshTokenRepository(), service.NewRoleService(pool, repository.NewRoleRepository()), authn.NewIssuer(priv))
	apiTokenSvc := service.NewUserAPITokenService(pool, repository.NewUserAPITokenRepository(), repository.NewUserRepository())
	h := handlers.NewAccountHandlers(authSvc, apiTokenSvc)
	r := newRouter(h.Routes)

	t.Run("missing name -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"name": "", "email": "someone@test.local"})
		req := withClaims(httptest.NewRequest("PUT", "/profile", bytes.NewReader(body)), tenantID, userID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("invalid JSON body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("PUT", "/profile", bytes.NewReader([]byte("{not-json"))), tenantID, userID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("email change without current password -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"name": "New Name", "email": "changed@test.local"})
		req := withClaims(httptest.NewRequest("PUT", "/profile", bytes.NewReader(body)), tenantID, userID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("valid update -- 200 with the updated user", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{
			"name": "New Name", "email": "changed2@test.local", "currentPassword": testutil.TestPassword,
		})
		req := withClaims(httptest.NewRequest("PUT", "/profile", bytes.NewReader(body)), tenantID, userID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "New Name")
		assert.Contains(t, rec.Body.String(), "changed2@test.local")
	})
}

func TestAccountHandlers_APITokens(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "analyst", nil)
	priv, err := authn.GenerateEphemeralKeyPair()
	require.NoError(t, err)
	authSvc := service.NewAuthService(pool, repository.NewTenantRepository(), repository.NewUserRepository(), repository.NewRefreshTokenRepository(), service.NewRoleService(pool, repository.NewRoleRepository()), authn.NewIssuer(priv))
	apiTokenSvc := service.NewUserAPITokenService(pool, repository.NewUserAPITokenRepository(), repository.NewUserRepository())
	h := handlers.NewAccountHandlers(authSvc, apiTokenSvc)
	r := newRouter(h.Routes)

	t.Run("missing name -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"name": ""})
		req := withClaims(httptest.NewRequest("POST", "/api-tokens", bytes.NewReader(body)), tenantID, userID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	var tokenID string
	t.Run("create -- 201 with the plaintext token, shown once", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"name": "CI script"})
		req := withClaims(httptest.NewRequest("POST", "/api-tokens", bytes.NewReader(body)), tenantID, userID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

		var resp struct {
			Token struct {
				ID         string `json:"id"`
				Name       string `json:"name"`
				TokenLast4 string `json:"tokenLast4"`
			} `json:"token"`
			Plaintext string `json:"plaintext"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.Equal(t, "CI script", resp.Token.Name)
		assert.True(t, strings.HasPrefix(resp.Plaintext, "pat_"))
		assert.Equal(t, resp.Plaintext[len(resp.Plaintext)-4:], resp.Token.TokenLast4)
		tokenID = resp.Token.ID
	})

	t.Run("list -- includes the created token, never the plaintext", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/api-tokens", nil), tenantID, userID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "CI script")
		assert.NotContains(t, rec.Body.String(), "pat_")
	})

	t.Run("revoke -- 204, then it disappears from an active-only view but stays listed", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("DELETE", "/api-tokens/"+tokenID, nil), tenantID, userID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusNoContent, rec.Code)

		listReq := withClaims(httptest.NewRequest("GET", "/api-tokens", nil), tenantID, userID, nil)
		listRec := doRequest(r, listReq)
		require.Equal(t, http.StatusOK, listRec.Code)
		assert.Contains(t, listRec.Body.String(), `"revokedAt"`, "the revoked token stays in the list, just marked revoked")
	})

	t.Run("malformed id -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("DELETE", "/api-tokens/not-a-uuid", nil), tenantID, userID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})
}
