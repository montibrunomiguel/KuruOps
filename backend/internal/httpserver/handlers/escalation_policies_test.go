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

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/httpserver/handlers"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func newEscalationPolicyHandlerFixture(t *testing.T) (h *handlers.EscalationPolicyHandlers, tenantID uuid.UUID, scheduleID uuid.UUID) {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	tenantID = testutil.NewTenant(t)

	onCallScheduleRepo := repository.NewOnCallScheduleRepository()
	onCallSvc := service.NewOnCallScheduleService(pool, onCallScheduleRepo, repository.NewUserRepository(), repository.NewTenantRepository())
	sched, err := onCallSvc.Create(t.Context(), tenantID, domain.SaveOnCallScheduleInput{
		Name: "Primary", HandoverAt: time.Now(), PeriodDays: 7, ConcurrentShifts: 1, WorkingHoursMode: domain.OnCallWorkingHoursAllDay,
	})
	require.NoError(t, err)
	scheduleID = sched.ID

	userSvc := service.NewUserService(pool, repository.NewUserRepository())
	svc := service.NewEscalationPolicyService(pool, repository.NewEscalationPolicyRepository(), onCallScheduleRepo, onCallSvc, userSvc, secrets.NewEnvStore())
	h = handlers.NewEscalationPolicyHandlers(svc)
	return h, tenantID, scheduleID
}

func TestEscalationPolicyHandlers(t *testing.T) {
	h, tenantID, scheduleID := newEscalationPolicyHandlerFixture(t)
	r := newRouter(h.Routes)

	t.Run("list before any policy -- 200 with an empty array", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "[]\n", rec.Body.String())
	})

	t.Run("save with no steps -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"severity": "critical", "steps": []any{}})
		req := withClaims(httptest.NewRequest("PUT", "/", bytes.NewReader(body)), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("save with a non-positive delay -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"severity": "critical",
			"steps": []any{
				map[string]any{"scheduleId": scheduleID, "delayMinutes": 0, "channelType": "pagerduty", "destination": "key"},
			},
		})
		req := withClaims(httptest.NewRequest("PUT", "/", bytes.NewReader(body)), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("save with no destination on initial create -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"severity": "critical",
			"steps": []any{
				map[string]any{"scheduleId": scheduleID, "delayMinutes": 15, "channelType": "pagerduty"},
			},
		})
		req := withClaims(httptest.NewRequest("PUT", "/", bytes.NewReader(body)), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("save with an unknown schedule id -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"severity": "critical",
			"steps": []any{
				map[string]any{"scheduleId": uuid.New(), "delayMinutes": 15, "channelType": "pagerduty", "destination": "key"},
			},
		})
		req := withClaims(httptest.NewRequest("PUT", "/", bytes.NewReader(body)), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	saveBody, _ := json.Marshal(map[string]any{
		"severity": "critical",
		"steps": []any{
			map[string]any{"scheduleId": scheduleID, "delayMinutes": 15, "channelType": "pagerduty", "destination": "R0UTING-KEY"},
			map[string]any{"scheduleId": scheduleID, "delayMinutes": 30, "channelType": "webhook", "destination": "https://hooks.example.invalid/x"},
		},
	})
	saveReq := withClaims(httptest.NewRequest("PUT", "/", bytes.NewReader(saveBody)), tenantID, uuid.New(), nil)
	saveRec := doRequest(r, saveReq)
	require.Equal(t, http.StatusOK, saveRec.Code)

	var saved domain.EscalationPolicy
	require.NoError(t, json.Unmarshal(saveRec.Body.Bytes(), &saved))
	require.Len(t, saved.Steps, 2)
	assert.Equal(t, 15, saved.Steps[0].DelayMinutes)
	assert.Equal(t, domain.EscalationChannelPagerDuty, saved.Steps[0].ChannelType)
	assert.Equal(t, 30, saved.Steps[1].DelayMinutes)
	assert.Equal(t, "Primary", saved.Steps[0].ScheduleName)

	t.Run("the response never leaks a destination secret ref", func(t *testing.T) {
		assert.NotContains(t, saveRec.Body.String(), "R0UTING-KEY")
	})

	t.Run("re-saving with a blank destination at an existing position keeps its secret", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"severity": "critical",
			"steps": []any{
				map[string]any{"scheduleId": scheduleID, "delayMinutes": 20, "channelType": "pagerduty", "destination": ""},
				map[string]any{"scheduleId": scheduleID, "delayMinutes": 30, "channelType": "webhook", "destination": ""},
			},
		})
		req := withClaims(httptest.NewRequest("PUT", "/", bytes.NewReader(body)), tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var updated domain.EscalationPolicy
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &updated))
		assert.Equal(t, 20, updated.Steps[0].DelayMinutes)
	})

	t.Run("a brand-new position with a blank destination -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"severity": "critical",
			"steps": []any{
				map[string]any{"scheduleId": scheduleID, "delayMinutes": 20, "channelType": "pagerduty", "destination": ""},
				map[string]any{"scheduleId": scheduleID, "delayMinutes": 30, "channelType": "webhook", "destination": ""},
				map[string]any{"scheduleId": scheduleID, "delayMinutes": 45, "channelType": "slack", "destination": ""},
			},
		})
		req := withClaims(httptest.NewRequest("PUT", "/", bytes.NewReader(body)), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("list after save reflects the policy", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		var policies []domain.EscalationPolicy
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &policies))
		require.Len(t, policies, 1)
		assert.Len(t, policies[0].Steps, 2)
	})

	t.Run("test an out-of-range step position -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"severity": "critical", "stepPosition": 5})
		req := withClaims(httptest.NewRequest("POST", "/test", bytes.NewReader(body)), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("delete removes the policy", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("DELETE", "/"+saved.ID.String(), nil), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)

		listReq := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, uuid.New(), nil)
		listRec := doRequest(r, listReq)
		assert.Equal(t, "[]\n", listRec.Body.String())
	})

	t.Run("delete with an invalid id -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("DELETE", "/not-a-uuid", nil), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("save invalid JSON body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("PUT", "/", bytes.NewReader([]byte("{not-json"))), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})
}

func TestEscalationPolicyHandlers_List_MissingTenantContext(t *testing.T) {
	h := handlers.NewEscalationPolicyHandlers(nil)
	r := newRouter(h.Routes)

	req := httptest.NewRequest("GET", "/", nil)
	assert.Equal(t, http.StatusUnauthorized, doRequest(r, req).Code)
}
