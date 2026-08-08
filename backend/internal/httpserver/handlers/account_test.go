package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
	h := handlers.NewAccountHandlers(authSvc)
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
	h := handlers.NewAccountHandlers(authSvc)
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
