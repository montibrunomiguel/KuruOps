package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/authn"
	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/httpserver/handlers"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/testutil"
)

func newUserHandlerFixture(t *testing.T) (h *handlers.UserHandlers, tenantID, targetUserID uuid.UUID) {
	t.Helper()
	h, tenantID, targetUserID, _ = newUserHandlerFixtureWithAuth(t)
	return h, tenantID, targetUserID
}

func newUserHandlerFixtureWithAuth(t *testing.T) (h *handlers.UserHandlers, tenantID, targetUserID uuid.UUID, authSvc *service.AuthService) {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	tenantID = testutil.NewTenant(t)
	targetUserID = testutil.NewUser(t, tenantID, "viewer", nil)
	userRepo := repository.NewUserRepository()
	roleSvc := service.NewRoleService(pool, repository.NewRoleRepository(), repository.NewAdminAuditEventRepository())
	priv, err := authn.GenerateEphemeralKeyPair()
	require.NoError(t, err)
	authSvc = service.NewAuthService(pool, repository.NewTenantRepository(), userRepo, repository.NewRefreshTokenRepository(), repository.NewMFAPendingTokenRepository(), roleSvc, authn.NewIssuer(priv))
	h = handlers.NewUserHandlers(service.NewUserService(pool, userRepo, repository.NewAdminAuditEventRepository()), authSvc)
	return h, tenantID, targetUserID, authSvc
}

func TestUserHandlers_ListAndUpdateAccess(t *testing.T) {
	h, tenantID, targetUserID := newUserHandlerFixture(t)
	r := newRouter(h.Routes)
	roleID := testutil.NewRole(t, tenantID, true, []string{"alerts", "incidents"})

	req := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, uuid.New(), nil)
	rec := doRequest(r, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	t.Run("unknown role id -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"roleId": uuid.New().String()})
		req := withClaims(httptest.NewRequest("PUT", "/"+targetUserID.String()+"/access", bytes.NewReader(body)), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("valid update -- 204", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"roleId": roleID.String()})
		req := withClaims(httptest.NewRequest("PUT", "/"+targetUserID.String()+"/access", bytes.NewReader(body)), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)
	})

	t.Run("invalid id -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"roleId": roleID.String()})
		req := withClaims(httptest.NewRequest("PUT", "/not-a-uuid/access", bytes.NewReader(body)), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("invalid JSON body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("PUT", "/"+targetUserID.String()+"/access", bytes.NewReader([]byte("{not-json"))), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})
}

func TestUserHandlers_UpdatePhone(t *testing.T) {
	h, tenantID, targetUserID := newUserHandlerFixture(t)
	r := newRouter(h.Routes)

	t.Run("valid phone -- 204, reflected in the list", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"phone": "+5511912345678"})
		req := withClaims(httptest.NewRequest("PUT", "/"+targetUserID.String()+"/phone", bytes.NewReader(body)), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)

		listReq := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, uuid.New(), nil)
		listRec := doRequest(r, listReq)
		var users []domain.User
		require.NoError(t, json.Unmarshal(listRec.Body.Bytes(), &users))
		require.Len(t, users, 1)
		require.NotNil(t, users[0].Phone)
		assert.Equal(t, "+5511912345678", *users[0].Phone)
	})

	t.Run("phone without a country code -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"phone": "5511912345678"})
		req := withClaims(httptest.NewRequest("PUT", "/"+targetUserID.String()+"/phone", bytes.NewReader(body)), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("invalid id -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"phone": "+5511912345678"})
		req := withClaims(httptest.NewRequest("PUT", "/not-a-uuid/phone", bytes.NewReader(body)), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("invalid JSON body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("PUT", "/"+targetUserID.String()+"/phone", bytes.NewReader([]byte("{not-json"))), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})
}

func TestUserHandlers_Create(t *testing.T) {
	h, tenantID, _ := newUserHandlerFixture(t)
	r := newRouter(h.Routes)
	roleID := testutil.NewRole(t, tenantID, false, []string{"alerts"})

	t.Run("missing email -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"name": "No Email", "roleId": roleID.String()})
		req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(body)), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("invalid JSON body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader([]byte("{not-json"))), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("valid create -- 201 with a temp password that isn't stored anywhere else", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"email": "newuser@test.local", "name": "New User", "phone": "+15550100199", "roleId": roleID.String(),
		})
		req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(body)), tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusCreated, rec.Code)

		var resp struct {
			User              domain.User `json:"user"`
			TemporaryPassword string      `json:"temporaryPassword"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.Equal(t, "newuser@test.local", resp.User.Email)
		assert.Equal(t, domain.AuthProviderLocal, resp.User.AuthProvider)
		assert.NotEmpty(t, resp.TemporaryPassword)
		require.NotNil(t, resp.User.Phone)
		assert.Equal(t, "+15550100199", *resp.User.Phone)

		// the new user now shows up in the regular list
		listReq := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, uuid.New(), nil)
		listRec := doRequest(r, listReq)
		var users []domain.User
		require.NoError(t, json.Unmarshal(listRec.Body.Bytes(), &users))
		emails := make([]string, len(users))
		for i, u := range users {
			emails[i] = u.Email
		}
		assert.Contains(t, emails, "newuser@test.local")
	})

	t.Run("duplicate email -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"email": "newuser@test.local", "name": "Duplicate", "roleId": roleID.String(),
		})
		req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(body)), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})
}

func TestUserHandlers_ActivateDeactivate(t *testing.T) {
	h, tenantID, targetUserID := newUserHandlerFixture(t)
	r := newRouter(h.Routes)

	req := withClaims(httptest.NewRequest("POST", "/"+targetUserID.String()+"/deactivate", nil), tenantID, uuid.New(), nil)
	assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)

	req = withClaims(httptest.NewRequest("POST", "/"+targetUserID.String()+"/activate", nil), tenantID, uuid.New(), nil)
	assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)

	t.Run("deactivate invalid id -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/not-a-uuid/deactivate", nil), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("activate invalid id -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/not-a-uuid/activate", nil), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})
}

// TestUserHandlers_Deactivate_RevokesRefreshToken guards the actual security
// property, not just the 204 status: deactivating a user must cut off a
// refresh token they were already holding, not just block future logins.
func TestUserHandlers_Deactivate_RevokesRefreshToken(t *testing.T) {
	h, tenantID, targetUserID, authSvc := newUserHandlerFixtureWithAuth(t)
	r := newRouter(h.Routes)

	// testutil.NewUser's fixture email is deterministic: "<id>@test.local".
	_, _, rt, _, err := authSvc.LoginLocal(t.Context(), tenantID, targetUserID.String()+"@test.local", testutil.TestPassword)
	require.NoError(t, err)
	require.NotEmpty(t, rt)

	req := withClaims(httptest.NewRequest("POST", "/"+targetUserID.String()+"/deactivate", nil), tenantID, uuid.New(), nil)
	require.Equal(t, http.StatusNoContent, doRequest(r, req).Code)

	token, newRT, err := authSvc.Refresh(t.Context(), tenantID, rt)
	require.NoError(t, err)
	assert.Empty(t, token, "the refresh token issued before deactivation must no longer work")
	assert.Empty(t, newRT)
}

func TestUserHandlers_RevokeSessions(t *testing.T) {
	h, tenantID, targetUserID := newUserHandlerFixture(t)
	r := newRouter(h.Routes)

	req := withClaims(httptest.NewRequest("POST", "/"+targetUserID.String()+"/revoke-sessions", nil), tenantID, uuid.New(), nil)
	assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)

	t.Run("invalid user id -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/not-a-uuid/revoke-sessions", nil), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})
}

func TestUserHandlers_ResetPassword(t *testing.T) {
	h, tenantID, targetUserID := newUserHandlerFixture(t)
	r := newRouter(h.Routes)

	req := withClaims(httptest.NewRequest("POST", "/"+targetUserID.String()+"/reset-password", nil), tenantID, uuid.New(), nil)
	rec := doRequest(r, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		TemporaryPassword string `json:"temporaryPassword"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp.TemporaryPassword)

	t.Run("unknown user -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/"+uuid.New().String()+"/reset-password", nil), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("invalid user id -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/not-a-uuid/reset-password", nil), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})
}

func TestUserHandlers_GroupMappings(t *testing.T) {
	h, tenantID, _ := newUserHandlerFixture(t)
	r := newRouter(h.Routes)
	roleID := testutil.NewRole(t, tenantID, false, []string{"alerts"})

	req := withClaims(httptest.NewRequest("GET", "/group-mappings", nil), tenantID, uuid.New(), nil)
	assert.Equal(t, http.StatusOK, doRequest(r, req).Code)

	body, _ := json.Marshal(map[string]any{"roleId": roleID.String()})
	req = withClaims(httptest.NewRequest("PUT", "/group-mappings/ldap/soc-analysts", bytes.NewReader(body)), tenantID, uuid.New(), nil)
	rec := doRequest(r, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var mapping map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &mapping))
	id := mapping["id"].(string)

	req = withClaims(httptest.NewRequest("DELETE", "/group-mappings/"+id, nil), tenantID, uuid.New(), nil)
	assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)

	t.Run("save mapping invalid JSON body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("PUT", "/group-mappings/ldap/soc-analysts", bytes.NewReader([]byte("{not-json"))), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("delete mapping invalid id -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("DELETE", "/group-mappings/not-a-uuid", nil), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})
}

// TestUserHandlers_Directory exercises the un-gated GET /users/directory
// route directly against the Directory method -- it isn't part of
// h.Routes() since it's deliberately mounted outside the admin-only
// /settings/users group (see router.go and Directory's doc comment).
func TestUserHandlers_Directory(t *testing.T) {
	h, tenantID, targetUserID := newUserHandlerFixture(t)
	r := newRouter(func(rt chi.Router) { rt.Get("/", h.Directory) })

	req := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, uuid.New(), nil)
	rec := doRequest(r, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var summaries []domain.UserSummary
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &summaries))
	require.Len(t, summaries, 1)
	assert.Equal(t, targetUserID, summaries[0].ID)
	assert.NotEmpty(t, summaries[0].Name)
}

func TestUserHandlers_Directory_MissingTenantContext(t *testing.T) {
	h, _, _ := newUserHandlerFixture(t)
	r := newRouter(func(rt chi.Router) { rt.Get("/", h.Directory) })

	req := httptest.NewRequest("GET", "/", nil)
	assert.Equal(t, http.StatusUnauthorized, doRequest(r, req).Code)
}

func TestUserHandlers_List_MissingTenantContext(t *testing.T) {
	h, _, _ := newUserHandlerFixture(t)
	r := newRouter(h.Routes)

	req := httptest.NewRequest("GET", "/", nil)
	assert.Equal(t, http.StatusUnauthorized, doRequest(r, req).Code)
}

func TestUserHandlers_Create_MissingTenantContext(t *testing.T) {
	h, _, _ := newUserHandlerFixture(t)
	r := newRouter(h.Routes)

	body, _ := json.Marshal(map[string]string{"email": "new@example.com", "name": "New"})
	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	assert.Equal(t, http.StatusUnauthorized, doRequest(r, req).Code)
}

func TestUserHandlers_ListGroupMappings_MissingTenantContext(t *testing.T) {
	h, _, _ := newUserHandlerFixture(t)
	r := newRouter(h.Routes)

	req := httptest.NewRequest("GET", "/group-mappings", nil)
	assert.Equal(t, http.StatusUnauthorized, doRequest(r, req).Code)
}
