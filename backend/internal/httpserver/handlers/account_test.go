package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/authn"
	"github.com/kuruops/kuruops/internal/httpserver/handlers"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/secrets"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/testutil"
)

func TestAccountHandlers_ChangePassword(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "analyst", nil)
	priv, err := authn.GenerateEphemeralKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	authSvc := service.NewAuthService(pool, repository.NewTenantRepository(), repository.NewUserRepository(), repository.NewRefreshTokenRepository(), repository.NewMFAPendingTokenRepository(), service.NewRoleService(pool, repository.NewRoleRepository(), repository.NewAdminAuditEventRepository()), authn.NewIssuer(priv), secrets.NewEnvStore())
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
	authSvc := service.NewAuthService(pool, repository.NewTenantRepository(), repository.NewUserRepository(), repository.NewRefreshTokenRepository(), repository.NewMFAPendingTokenRepository(), service.NewRoleService(pool, repository.NewRoleRepository(), repository.NewAdminAuditEventRepository()), authn.NewIssuer(priv), secrets.NewEnvStore())
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
			"name": "New Name", "email": "changed2@test.local", "phone": "+15550100199", "currentPassword": testutil.TestPassword,
		})
		req := withClaims(httptest.NewRequest("PUT", "/profile", bytes.NewReader(body)), tenantID, userID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "New Name")
		assert.Contains(t, rec.Body.String(), "changed2@test.local")
		assert.Contains(t, rec.Body.String(), "+15550100199")
	})

	t.Run("a request that omits phone entirely leaves the previously-set phone untouched", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"name": "Another Name", "email": "changed2@test.local"})
		req := withClaims(httptest.NewRequest("PUT", "/profile", bytes.NewReader(body)), tenantID, userID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "Another Name")
		assert.Contains(t, rec.Body.String(), "+15550100199", "phone must survive a request that never mentioned it")
	})

	t.Run("a phone without a country code is rejected with 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"name": "New Name", "email": "changed2@test.local", "phone": "5511912345678"})
		req := withClaims(httptest.NewRequest("PUT", "/profile", bytes.NewReader(body)), tenantID, userID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestAccountHandlers_APITokens(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "analyst", nil)
	priv, err := authn.GenerateEphemeralKeyPair()
	require.NoError(t, err)
	authSvc := service.NewAuthService(pool, repository.NewTenantRepository(), repository.NewUserRepository(), repository.NewRefreshTokenRepository(), repository.NewMFAPendingTokenRepository(), service.NewRoleService(pool, repository.NewRoleRepository(), repository.NewAdminAuditEventRepository()), authn.NewIssuer(priv), secrets.NewEnvStore())
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

func TestAccountHandlers_MFA(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "analyst", nil)
	priv, err := authn.GenerateEphemeralKeyPair()
	require.NoError(t, err)
	authSvc := service.NewAuthService(pool, repository.NewTenantRepository(), repository.NewUserRepository(), repository.NewRefreshTokenRepository(), repository.NewMFAPendingTokenRepository(), service.NewRoleService(pool, repository.NewRoleRepository(), repository.NewAdminAuditEventRepository()), authn.NewIssuer(priv), secrets.NewEnvStore())
	apiTokenSvc := service.NewUserAPITokenService(pool, repository.NewUserAPITokenRepository(), repository.NewUserRepository())
	h := handlers.NewAccountHandlers(authSvc, apiTokenSvc)
	r := newRouter(h.Routes)

	var secret string
	t.Run("enroll -- 200 with a secret and an otpauth URL, nothing persisted yet", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/mfa/enroll", nil), tenantID, userID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)

		var resp struct {
			Secret     string `json:"secret"`
			OtpauthURL string `json:"otpauthUrl"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		require.NotEmpty(t, resp.Secret)
		assert.Contains(t, resp.OtpauthURL, "otpauth://totp/")
		secret = resp.Secret

		// GenerateMFAEnrollment never writes to the DB -- confirm login
		// isn't gated behind MFA yet by checking LoginLocal returns no
		// pending token for this account.
		_, _, _, pendingToken, err := authSvc.LoginLocal(t.Context(), tenantID, userID.String()+"@test.local", testutil.TestPassword)
		require.NoError(t, err)
		assert.Empty(t, pendingToken)
	})

	t.Run("confirm with a wrong code -- 400, still not active", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"secret": secret, "code": "000000"})
		req := withClaims(httptest.NewRequest("PUT", "/mfa", bytes.NewReader(body)), tenantID, userID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("confirm malformed body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("PUT", "/mfa", bytes.NewReader([]byte("not json"))), tenantID, userID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("confirm with the correct code -- 200 with a fresh token, activates it", func(t *testing.T) {
		code, err := totp.GenerateCode(secret, time.Now())
		require.NoError(t, err)
		body, _ := json.Marshal(map[string]string{"secret": secret, "code": code})
		req := withClaims(httptest.NewRequest("PUT", "/mfa", bytes.NewReader(body)), tenantID, userID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var resp map[string]string
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.NotEmpty(t, resp["token"])

		_, _, _, pendingToken, err := authSvc.LoginLocal(t.Context(), tenantID, userID.String()+"@test.local", testutil.TestPassword)
		require.NoError(t, err)
		assert.NotEmpty(t, pendingToken, "login must now require the second factor")
	})

	t.Run("disable with the wrong password -- 400, stays enrolled", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"currentPassword": "wrong"})
		req := withClaims(httptest.NewRequest("DELETE", "/mfa", bytes.NewReader(body)), tenantID, userID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)

		_, _, _, pendingToken, err := authSvc.LoginLocal(t.Context(), tenantID, userID.String()+"@test.local", testutil.TestPassword)
		require.NoError(t, err)
		assert.NotEmpty(t, pendingToken)
	})

	t.Run("disable malformed body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("DELETE", "/mfa", bytes.NewReader([]byte("not json"))), tenantID, userID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("disable with the correct password -- 200 with a fresh token, login no longer requires a second factor", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"currentPassword": testutil.TestPassword})
		req := withClaims(httptest.NewRequest("DELETE", "/mfa", bytes.NewReader(body)), tenantID, userID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var resp map[string]string
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.NotEmpty(t, resp["token"])

		user, _, _, pendingToken, err := authSvc.LoginLocal(t.Context(), tenantID, userID.String()+"@test.local", testutil.TestPassword)
		require.NoError(t, err)
		require.NotNil(t, user)
		assert.Empty(t, pendingToken)
	})
}
