package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

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
	patSvc := service.NewPersonalAccessTokenService(pool, repository.NewTenantRepository(), repository.NewUserRepository(), repository.NewPersonalAccessTokenRepository())
	h := handlers.NewAccountHandlers(authSvc, patSvc)
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
	patSvc := service.NewPersonalAccessTokenService(pool, repository.NewTenantRepository(), repository.NewUserRepository(), repository.NewPersonalAccessTokenRepository())
	h := handlers.NewAccountHandlers(authSvc, patSvc)
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

func TestAccountHandlers_Tokens(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	// PersonalAccessTokenService.Resolve authenticates a bearer token before
	// any tenant is known (see its doc comment), so it always resolves
	// against TenantRepository.GetDefault -- "the oldest tenant row" --
	// same as every other login path. A tenant testutil.NewTenant(t) just
	// created is never that row in this long-lived shared test database
	// (plenty of older tenants already exist from other tests), so the
	// subtests here that exercise Resolve() need the user under the *real*
	// default tenant, not an ad-hoc one, or Resolve looks the token up in
	// the wrong tenant's RLS scope and always reports not-found.
	tenant, err := repository.NewTenantRepository().GetDefault(t.Context(), pool)
	if err != nil || tenant == nil {
		t.Fatalf("resolve default tenant: %v (tenant=%v)", err, tenant)
	}
	tenantID := tenant.ID
	userID := testutil.NewUser(t, tenantID, "analyst", nil)
	priv, err := authn.GenerateEphemeralKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	authSvc := service.NewAuthService(pool, repository.NewTenantRepository(), repository.NewUserRepository(), repository.NewRefreshTokenRepository(), service.NewRoleService(pool, repository.NewRoleRepository()), authn.NewIssuer(priv))
	patSvc := service.NewPersonalAccessTokenService(pool, repository.NewTenantRepository(), repository.NewUserRepository(), repository.NewPersonalAccessTokenRepository())
	h := handlers.NewAccountHandlers(authSvc, patSvc)
	r := newRouter(h.Routes)

	t.Run("missing name -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"name": ""})
		req := withClaims(httptest.NewRequest("POST", "/tokens", bytes.NewReader(body)), tenantID, userID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("create returns the plaintext token once, list never includes it", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"name": "CI script"})
		createReq := withClaims(httptest.NewRequest("POST", "/tokens", bytes.NewReader(body)), tenantID, userID, nil)
		createRec := doRequest(r, createReq)
		assert.Equal(t, http.StatusCreated, createRec.Code)

		var createResp map[string]json.RawMessage
		assert.NoError(t, json.Unmarshal(createRec.Body.Bytes(), &createResp))
		var plaintext string
		assert.NoError(t, json.Unmarshal(createResp["plaintext"], &plaintext))
		assert.Contains(t, plaintext, "pat_")

		listReq := withClaims(httptest.NewRequest("GET", "/tokens", nil), tenantID, userID, nil)
		listRec := doRequest(r, listReq)
		assert.Equal(t, http.StatusOK, listRec.Code)
		assert.Contains(t, listRec.Body.String(), "CI script")
		assert.NotContains(t, listRec.Body.String(), plaintext)

		// The token actually authenticates -- confirms Resolve() round-trips
		// correctly against what Create() just persisted.
		resolvedTenant, resolvedUser, _, _, _, ok, err := patSvc.Resolve(t.Context(), plaintext)
		assert.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, tenantID, resolvedTenant)
		assert.Equal(t, userID, resolvedUser)
	})

	t.Run("revoking a token makes it stop authenticating", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"name": "to be revoked"})
		createReq := withClaims(httptest.NewRequest("POST", "/tokens", bytes.NewReader(body)), tenantID, userID, nil)
		createRec := doRequest(r, createReq)

		var createResp map[string]json.RawMessage
		assert.NoError(t, json.Unmarshal(createRec.Body.Bytes(), &createResp))
		var plaintext string
		assert.NoError(t, json.Unmarshal(createResp["plaintext"], &plaintext))
		var tokenBody struct {
			ID string `json:"id"`
		}
		assert.NoError(t, json.Unmarshal(createResp["token"], &tokenBody))

		delReq := withClaims(httptest.NewRequest("DELETE", "/tokens/"+tokenBody.ID, nil), tenantID, userID, nil)
		assert.Equal(t, http.StatusNoContent, doRequest(r, delReq).Code)

		_, _, _, _, _, ok, err := patSvc.Resolve(t.Context(), plaintext)
		assert.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("revoking an unknown token id -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("DELETE", "/tokens/"+uuid.NewString(), nil), tenantID, userID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})
}
