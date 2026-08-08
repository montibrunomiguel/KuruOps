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

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/httpserver/handlers"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func newOnCallShiftHandlerFixture(t *testing.T) (h *handlers.OnCallShiftHandlers, tenantID uuid.UUID, analystID uuid.UUID) {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	tenantID = testutil.NewTenant(t)
	analystID = testutil.NewUser(t, tenantID, "analyst", nil)
	svc := service.NewOnCallShiftService(pool, repository.NewOnCallShiftRepository(), repository.NewUserRepository(), repository.NewTenantRepository())
	h = handlers.NewOnCallShiftHandlers(svc)
	return h, tenantID, analystID
}

func TestOnCallShiftHandlers_ListDefaultsToEmptyUTC(t *testing.T) {
	h, tenantID, _ := newOnCallShiftHandlerFixture(t)
	r := newRouter(h.Routes)

	req := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, uuid.New(), nil)
	rec := doRequest(r, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Timezone string               `json:"timezone"`
		Shifts   []domain.OnCallShift `json:"shifts"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "UTC", resp.Timezone)
	assert.Empty(t, resp.Shifts)
}

func TestOnCallShiftHandlers_CreateAndDelete(t *testing.T) {
	h, tenantID, analystID := newOnCallShiftHandlerFixture(t)
	r := newRouter(h.Routes)

	t.Run("invalid weekday -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"userId": analystID, "weekday": 9, "startMinute": 0, "endMinute": 60})
		req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(body)), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	body, _ := json.Marshal(map[string]any{"userId": analystID, "weekday": 1, "startMinute": 540, "endMinute": 1020})
	req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(body)), tenantID, uuid.New(), nil)
	rec := doRequest(r, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	var shift domain.OnCallShift
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &shift))
	assert.Equal(t, analystID, shift.UserID)

	listReq := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, uuid.New(), nil)
	listRec := doRequest(r, listReq)
	var resp struct {
		Shifts []domain.OnCallShift `json:"shifts"`
	}
	require.NoError(t, json.Unmarshal(listRec.Body.Bytes(), &resp))
	require.Len(t, resp.Shifts, 1)

	delReq := withClaims(httptest.NewRequest("DELETE", "/"+shift.ID.String(), nil), tenantID, uuid.New(), nil)
	assert.Equal(t, http.StatusNoContent, doRequest(r, delReq).Code)
}

func TestOnCallShiftHandlers_SetTimezone(t *testing.T) {
	h, tenantID, _ := newOnCallShiftHandlerFixture(t)
	r := newRouter(h.Routes)

	t.Run("unknown timezone -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"timezone": "Not/A_Real_Zone"})
		req := withClaims(httptest.NewRequest("PUT", "/timezone", bytes.NewReader(body)), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	body, _ := json.Marshal(map[string]string{"timezone": "America/Sao_Paulo"})
	req := withClaims(httptest.NewRequest("PUT", "/timezone", bytes.NewReader(body)), tenantID, uuid.New(), nil)
	assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)

	listReq := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, uuid.New(), nil)
	listRec := doRequest(r, listReq)
	var resp struct {
		Timezone string `json:"timezone"`
	}
	require.NoError(t, json.Unmarshal(listRec.Body.Bytes(), &resp))
	assert.Equal(t, "America/Sao_Paulo", resp.Timezone)
}

func TestOnCallShiftHandlers_ValidationErrors(t *testing.T) {
	h, tenantID, _ := newOnCallShiftHandlerFixture(t)
	r := newRouter(h.Routes)

	t.Run("create invalid JSON body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader([]byte("{not-json"))), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("delete invalid id -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("DELETE", "/not-a-uuid", nil), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("set timezone invalid JSON body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("PUT", "/timezone", bytes.NewReader([]byte("{not-json"))), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})
}

func TestOnCallShiftHandlers_List_MissingTenantContext(t *testing.T) {
	h := handlers.NewOnCallShiftHandlers(nil)
	r := newRouter(h.Routes)

	req := httptest.NewRequest("GET", "/", nil)
	assert.Equal(t, http.StatusUnauthorized, doRequest(r, req).Code)
}
