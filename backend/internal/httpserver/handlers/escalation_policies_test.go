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
	"github.com/argusops/argusops/internal/secrets"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func TestEscalationPolicyHandlers(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	svc := service.NewEscalationPolicyService(pool, repository.NewEscalationPolicyRepository(), secrets.NewEnvStore())
	h := handlers.NewEscalationPolicyHandlers(svc)
	r := newRouter(h.Routes)

	t.Run("list before any policy -- 200 with an empty array", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "[]\n", rec.Body.String())
	})

	t.Run("save with a non-positive threshold -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"severity": "critical", "unacknowledgedAfterMinutes": 0, "channelType": "pagerduty", "destination": "key",
		})
		req := withClaims(httptest.NewRequest("PUT", "/", bytes.NewReader(body)), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("save with no destination on initial create -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"severity": "critical", "unacknowledgedAfterMinutes": 15, "channelType": "pagerduty",
		})
		req := withClaims(httptest.NewRequest("PUT", "/", bytes.NewReader(body)), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	saveBody, _ := json.Marshal(map[string]any{
		"severity": "critical", "unacknowledgedAfterMinutes": 15, "channelType": "pagerduty", "destination": "R0UTING-KEY",
	})
	saveReq := withClaims(httptest.NewRequest("PUT", "/", bytes.NewReader(saveBody)), tenantID, uuid.New(), nil)
	saveRec := doRequest(r, saveReq)
	require.Equal(t, http.StatusOK, saveRec.Code)

	var saved domain.EscalationPolicy
	require.NoError(t, json.Unmarshal(saveRec.Body.Bytes(), &saved))
	assert.Equal(t, 15, saved.UnacknowledgedAfterMinutes)
	assert.Equal(t, domain.EscalationChannelPagerDuty, saved.ChannelType)

	t.Run("the response never leaks the destination secret ref", func(t *testing.T) {
		assert.NotContains(t, saveRec.Body.String(), "R0UTING-KEY")
	})

	t.Run("list after save reflects the policy", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		var policies []domain.EscalationPolicy
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &policies))
		require.Len(t, policies, 1)
		assert.Equal(t, 15, policies[0].UnacknowledgedAfterMinutes)
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
}
