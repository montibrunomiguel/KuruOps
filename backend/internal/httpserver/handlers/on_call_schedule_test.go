package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/httpserver/handlers"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/testutil"
)

func newOnCallScheduleHandlerFixture(t *testing.T) (h *handlers.OnCallScheduleHandlers, tenantID, actorID uuid.UUID) {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	tenantID = testutil.NewTenant(t)
	actorID = testutil.NewUser(t, tenantID, "admin", nil)
	h = handlers.NewOnCallScheduleHandlers(service.NewOnCallScheduleService(pool, repository.NewOnCallScheduleRepository(), repository.NewUserRepository(), repository.NewTenantRepository(), repository.NewAdminAuditEventRepository()))
	return h, tenantID, actorID
}

func validScheduleBody(name string, participantIDs ...uuid.UUID) []byte {
	ids := make([]string, len(participantIDs))
	for i, id := range participantIDs {
		ids[i] = id.String()
	}
	body, _ := json.Marshal(map[string]any{
		"name":             name,
		"participantIds":   ids,
		"handoverAt":       time.Now().Format(time.RFC3339),
		"periodDays":       7,
		"concurrentShifts": 1,
		"workingHoursMode": "all_day",
	})
	return body
}

func TestOnCallScheduleHandlers_ListMissingTenantContext(t *testing.T) {
	h := handlers.NewOnCallScheduleHandlers(nil)
	r := newRouter(h.Routes)

	req := httptest.NewRequest("GET", "/", nil)
	assert.Equal(t, http.StatusUnauthorized, doRequest(r, req).Code)
}

func TestOnCallScheduleHandlers_ListAutoCreatesDefault(t *testing.T) {
	h, tenantID, actorID := newOnCallScheduleHandlerFixture(t)
	r := newRouter(h.Routes)

	req := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, actorID, nil)
	rec := doRequest(r, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var schedules []domain.OnCallSchedule
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &schedules))
	require.Len(t, schedules, 1)
	assert.Equal(t, "Primary On-Call", schedules[0].Name)
	assert.True(t, schedules[0].IsDefault)
	assert.Equal(t, "UTC", schedules[0].Timezone)
}

func TestOnCallScheduleHandlers_CreateGetUpdateDelete(t *testing.T) {
	h, tenantID, actorID := newOnCallScheduleHandlerFixture(t)
	r := newRouter(h.Routes)
	alice := testutil.NewUser(t, tenantID, "analyst", nil)

	t.Run("invalid JSON body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader([]byte("{not-json"))), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("blank name -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"name": "", "periodDays": 7, "concurrentShifts": 1, "workingHoursMode": "all_day"})
		req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(body)), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(validScheduleBody("Primary On-Call", alice))), tenantID, actorID, nil)
	rec := doRequest(r, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	var created domain.OnCallSchedule
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	require.Len(t, created.Participants, 1)
	assert.Equal(t, alice, created.Participants[0].UserID)
	assert.True(t, created.IsDefault, "the tenant's first schedule becomes the default")

	t.Run("get by id reflects the created schedule", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/"+created.ID.String(), nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var got domain.OnCallSchedule
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
		assert.Equal(t, created.ID, got.ID)
	})

	t.Run("get unknown id -- 404", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/"+uuid.New().String(), nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusNotFound, doRequest(r, req).Code)
	})

	t.Run("update", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("PUT", "/"+created.ID.String(), bytes.NewReader(validScheduleBody("Renamed", alice))), tenantID, actorID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var updated domain.OnCallSchedule
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &updated))
		assert.Equal(t, "Renamed", updated.Name)
		assert.True(t, updated.IsDefault, "update must not clear is_default")
	})

	t.Run("delete invalid id -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("DELETE", "/not-a-uuid", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("delete the only schedule -- 204", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("DELETE", "/"+created.ID.String(), nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)
	})
}

func TestOnCallScheduleHandlers_SetDefault(t *testing.T) {
	h, tenantID, actorID := newOnCallScheduleHandlerFixture(t)
	r := newRouter(h.Routes)

	createReq := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(validScheduleBody("A"))), tenantID, actorID, nil)
	var a domain.OnCallSchedule
	require.NoError(t, json.Unmarshal(doRequest(r, createReq).Body.Bytes(), &a))
	require.True(t, a.IsDefault)

	createReq2 := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(validScheduleBody("B"))), tenantID, actorID, nil)
	var b domain.OnCallSchedule
	require.NoError(t, json.Unmarshal(doRequest(r, createReq2).Body.Bytes(), &b))
	require.False(t, b.IsDefault)

	t.Run("invalid id -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/not-a-uuid/default", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	req := withClaims(httptest.NewRequest("POST", "/"+b.ID.String()+"/default", nil), tenantID, actorID, nil)
	assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)

	getA := withClaims(httptest.NewRequest("GET", "/"+a.ID.String(), nil), tenantID, actorID, nil)
	var gotA domain.OnCallSchedule
	require.NoError(t, json.Unmarshal(doRequest(r, getA).Body.Bytes(), &gotA))
	assert.False(t, gotA.IsDefault, "the previous default must be cleared")

	t.Run("cannot delete the new default while the other schedule still exists", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("DELETE", "/"+b.ID.String(), nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})
}

func TestOnCallScheduleHandlers_SetTimezone(t *testing.T) {
	h, tenantID, actorID := newOnCallScheduleHandlerFixture(t)
	r := newRouter(h.Routes)

	t.Run("invalid JSON body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("PUT", "/timezone", bytes.NewReader([]byte("{not-json"))), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("unknown timezone -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"timezone": "Nowhere/Fake"})
		req := withClaims(httptest.NewRequest("PUT", "/timezone", bytes.NewReader(body)), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("valid timezone -- 204", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"timezone": "America/Sao_Paulo"})
		req := withClaims(httptest.NewRequest("PUT", "/timezone", bytes.NewReader(body)), tenantID, actorID, nil)
		assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)
	})
}

func TestOnCallScheduleHandlers_Overrides(t *testing.T) {
	h, tenantID, actorID := newOnCallScheduleHandlerFixture(t)
	r := newRouter(h.Routes)
	alice := testutil.NewUser(t, tenantID, "analyst", nil)

	createReq := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(validScheduleBody("Primary"))), tenantID, actorID, nil)
	var sched domain.OnCallSchedule
	require.NoError(t, json.Unmarshal(doRequest(r, createReq).Body.Bytes(), &sched))

	t.Run("invalid JSON body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/"+sched.ID.String()+"/overrides", bytes.NewReader([]byte("{not-json"))), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("unknown schedule id -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"userId": alice, "date": "2026-03-01"})
		req := withClaims(httptest.NewRequest("POST", "/"+uuid.New().String()+"/overrides", bytes.NewReader(body)), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	body, _ := json.Marshal(map[string]any{"userId": alice, "date": "2026-03-01"})
	req := withClaims(httptest.NewRequest("POST", "/"+sched.ID.String()+"/overrides", bytes.NewReader(body)), tenantID, actorID, nil)
	rec := doRequest(r, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	var override domain.OnCallOverride
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &override))
	assert.Equal(t, alice, override.UserID)
	assert.Equal(t, "2026-03-01", override.Date)

	t.Run("delete invalid id -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("DELETE", "/"+sched.ID.String()+"/overrides/not-a-uuid", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("delete removes it -- 204", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("DELETE", "/"+sched.ID.String()+"/overrides/"+override.ID.String(), nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)
	})
}
