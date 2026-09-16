package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/httpserver/handlers"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/sessioncookie"
	"github.com/kuruops/kuruops/internal/testutil"
)

// localUser is an account created just for this test, with a password it
// knows.
//
// Using admin@kuruops.local would match the real scenario more closely --
// the forced first-login change is exactly where this bug lives -- but
// kuruops_test is shared by every package in the suite, and changing that
// account's password here breaks every other test that signs in as it (it
// did, on the first run). The mechanism is identical for any local account.
type localUser struct {
	tenantID uuid.UUID
	userID   uuid.UUID
	email    string
	password string
}

func newLocalUser(t *testing.T, authSvc *service.AuthService) localUser {
	t.Helper()
	pool := testutil.RequireTestDB(t)

	tenant, err := authSvc.ResolveDefaultTenant(t.Context())
	require.NoError(t, err)
	require.NotNil(t, tenant, "the seed migration must have created a tenant")

	actorID := testutil.NewUser(t, tenant.ID, "admin", nil)
	roleID := testutil.NewRole(t, tenant.ID, false, []string{"alerts"})
	users := service.NewUserService(pool, repository.NewUserRepository(), repository.NewAdminAuditEventRepository())

	email := "pwchange-" + uuid.NewString() + "@test.local"
	created, temp, err := users.CreateLocal(t.Context(), tenant.ID, actorID, email, "Password Change", "", roleID)
	require.NoError(t, err)

	return localUser{tenantID: tenant.ID, userID: created.ID, email: email, password: temp}
}

// TestPasswordChangeKeepsTheCallersSession covers the first minute of every
// new deployment.
//
// A new account is forced to change its password before anything else
// unlocks, and that change revokes every refresh token for the user -- which
// it should: a password change is exactly when a stolen token must stop
// working. The one it must not silently kill is the caller's own. It used
// to: the response carried a new access token but no new cookie, so the user
// kept working on an in-memory token and was thrown back to the login screen
// by their first page reload, with nothing on screen explaining why.
//
// Found by installing the project from a clean clone and following the
// README, which is about the only way this shows up -- every other test here
// already has a session and never reloads a page.
func TestPasswordChangeKeepsTheCallersSession(t *testing.T) {
	h, authSvc := newAuthHandlersAndService(t)
	r := newRouter(h.Routes)

	u := newLocalUser(t, authSvc)
	ar := newRouter(handlers.NewAccountHandlers(authSvc, nil, "https://kuruops.test").Routes)

	login := func(t *testing.T) string {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"email": u.email, "password": u.password})
		rec := doRequest(r, httptest.NewRequest("POST", "/login", bytes.NewReader(body)))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		return refreshCookie(t, rec)
	}

	// Two devices signed in as the same user.
	firstDevice := login(t)
	secondDevice := login(t)
	require.NotEqual(t, firstDevice, secondDevice)

	changeBody, _ := json.Marshal(map[string]string{
		"currentPassword": u.password,
		"newPassword":     "FirstLogin2026!",
	})
	req := withClaims(httptest.NewRequest("POST", "/change-password", bytes.NewReader(changeBody)), u.tenantID, u.userID, nil)
	changeRec := doRequest(ar, req)
	require.Equal(t, http.StatusOK, changeRec.Code, changeRec.Body.String())

	t.Run("the response carries a fresh refresh cookie", func(t *testing.T) {
		// Without this the caller has no way to rebuild a session after a
		// reload -- the access token lives in memory only.
		rotated := ""
		for _, c := range changeRec.Result().Cookies() {
			if c.Name == sessioncookie.Name {
				rotated = c.Value
			}
		}
		require.NotEmpty(t, rotated, "a password change must re-establish the caller's own session")
		assert.NotEqual(t, firstDevice, rotated, "and it must be a new token, not the revoked one")

		// This is the reload path.
		rec := doRequest(r, withRefreshCookie(httptest.NewRequest("POST", "/refresh", nil), rotated))
		assert.Equal(t, http.StatusOK, rec.Code, "reloading after a password change must not sign the user out")
	})

	t.Run("every other device is signed out", func(t *testing.T) {
		// The security half of the same change -- keeping the caller's
		// session must not have spared anyone else's.
		for name, cookie := range map[string]string{
			"the pre-change cookie from this device": firstDevice,
			"another device's cookie":                secondDevice,
		} {
			rec := doRequest(r, withRefreshCookie(httptest.NewRequest("POST", "/refresh", nil), cookie))
			assert.Equalf(t, http.StatusUnauthorized, rec.Code, "%s should be revoked", name)
		}
	})
}
