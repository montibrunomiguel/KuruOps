package handlers_test

import (
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

func TestDashboardHandlers_StatsAndFollowup(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	tagSvc := service.NewTagService(pool, repository.NewTagRepository())
	alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), tagSvc)
	incSvc := service.NewIncidentService(pool, repository.NewIncidentRepository(), tagSvc, repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository()))
	dashSvc := service.NewDashboardService(pool, repository.NewDashboardRepository(), alertSvc, incSvc)
	h := handlers.NewDashboardHandlers(dashSvc)

	t.Run("stats", func(t *testing.T) {
		r := newRouter(h.Routes)
		req := withClaims(httptest.NewRequest("GET", "/stats", nil), tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("stats without tenant context -- 401", func(t *testing.T) {
		r := newRouter(h.Routes)
		req := httptest.NewRequest("GET", "/stats", nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("followup", func(t *testing.T) {
		r := newRouter(h.FollowupRoutes)
		req := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("activity", func(t *testing.T) {
		r := newRouter(h.Routes)
		req := withClaims(httptest.NewRequest("GET", "/activity", nil), tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("activity without tenant context -- 401", func(t *testing.T) {
		r := newRouter(h.Routes)
		req := httptest.NewRequest("GET", "/activity", nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("stats parses alertSeverity/incidentSeverity query params into the filter", func(t *testing.T) {
		endpointID := testutil.NewWebhookEndpoint(t, tenantID)
		_, err := alertSvc.Ingest(t.Context(), tenantID, endpointID, domain.Alert{
			Title: "critical one", Source: "s", Severity: domain.SeverityCritical, Payload: json.RawMessage(`{}`),
		})
		require.NoError(t, err)
		_, err = alertSvc.Ingest(t.Context(), tenantID, endpointID, domain.Alert{
			Title: "low one", Source: "s", Severity: domain.SeverityLow, Payload: json.RawMessage(`{}`),
		})
		require.NoError(t, err)

		r := newRouter(h.Routes)
		req := withClaims(httptest.NewRequest("GET", "/stats?alertSeverity=critical", nil), tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)

		var stats struct {
			OpenAlerts int `json:"openAlerts"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &stats))
		assert.Equal(t, 1, stats.OpenAlerts, "only the critical alert matches alertSeverity=critical")
	})

	t.Run("activity parses kind query param to scope the feed", func(t *testing.T) {
		actorID := testutil.NewUser(t, tenantID, "analyst", nil)
		_, err := incSvc.Create(t.Context(), tenantID, actorID, domain.CreateIncidentInput{
			Title: "Kind filter test", Severity: domain.SeverityCritical, Priority: domain.PriorityP1,
		})
		require.NoError(t, err)

		r := newRouter(h.Routes)
		req := withClaims(httptest.NewRequest("GET", "/activity?kind=alert", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)

		var events []struct {
			Kind string `json:"kind"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &events))
		for _, e := range events {
			assert.Equal(t, "alert", e.Kind, "kind=alert must never include an incident event")
		}
	})
}
