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
	tagSvc := service.NewTagService(pool, repository.NewTagRepository(), repository.NewAdminAuditEventRepository())
	alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), tagSvc, repository.NewPlaybookRepository())
	incSvc := service.NewIncidentService(pool, repository.NewIncidentRepository(), tagSvc, repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository(), repository.NewAdminAuditEventRepository()))
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
		_, _, err := alertSvc.Ingest(t.Context(), tenantID, endpointID, domain.Alert{
			Title: "critical one", Source: "s", Severity: domain.SeverityCritical, Payload: json.RawMessage(`{}`),
		}, nil, 0)
		require.NoError(t, err)
		_, _, err = alertSvc.Ingest(t.Context(), tenantID, endpointID, domain.Alert{
			Title: "low one", Source: "s", Severity: domain.SeverityLow, Payload: json.RawMessage(`{}`),
		}, nil, 0)
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

	t.Run("stats parses a comma-separated alertSeverity into a multi-select OR filter", func(t *testing.T) {
		endpointID := testutil.NewWebhookEndpoint(t, tenantID)
		_, _, err := alertSvc.Ingest(t.Context(), tenantID, endpointID, domain.Alert{
			Title: "critical two", Source: "s", Severity: domain.SeverityCritical, Payload: json.RawMessage(`{}`),
		}, nil, 0)
		require.NoError(t, err)
		_, _, err = alertSvc.Ingest(t.Context(), tenantID, endpointID, domain.Alert{
			Title: "high one", Source: "s", Severity: domain.SeverityHigh, Payload: json.RawMessage(`{}`),
		}, nil, 0)
		require.NoError(t, err)
		_, _, err = alertSvc.Ingest(t.Context(), tenantID, endpointID, domain.Alert{
			Title: "medium one", Source: "s", Severity: domain.SeverityMedium, Payload: json.RawMessage(`{}`),
		}, nil, 0)
		require.NoError(t, err)

		r := newRouter(h.Routes)
		req := withClaims(httptest.NewRequest("GET", "/stats?alertSeverity=critical,high", nil), tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)

		var stats struct {
			AlertsBySeverity map[string]int `json:"alertsBySeverity"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &stats))
		assert.Equal(t, 2, stats.AlertsBySeverity["critical"], "both critical alerts ingested in this test file match")
		assert.Equal(t, 1, stats.AlertsBySeverity["high"])
		assert.Zero(t, stats.AlertsBySeverity["medium"], "medium must be excluded -- it wasn't in the comma-separated list")
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

func TestDashboardHandlers_Stats_MoreFilters(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)
	tagSvc := service.NewTagService(pool, repository.NewTagRepository(), repository.NewAdminAuditEventRepository())
	alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), tagSvc, repository.NewPlaybookRepository())
	incSvc := service.NewIncidentService(pool, repository.NewIncidentRepository(), tagSvc, repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository(), repository.NewAdminAuditEventRepository()))
	dashSvc := service.NewDashboardService(pool, repository.NewDashboardRepository(), alertSvc, incSvc)
	h := handlers.NewDashboardHandlers(dashSvc)
	r := newRouter(h.Routes)

	endpointID := testutil.NewWebhookEndpoint(t, tenantID)
	_, _, err := alertSvc.Ingest(t.Context(), tenantID, endpointID, domain.Alert{
		Title: "open one", Source: "wazuh", Severity: domain.SeverityHigh, Payload: json.RawMessage(`{}`),
	}, nil, 0)
	require.NoError(t, err)
	_, err = incSvc.Create(t.Context(), tenantID, actorID, domain.CreateIncidentInput{
		Title: "an incident", Severity: domain.SeverityHigh, Priority: domain.PriorityP2,
	})
	require.NoError(t, err)

	t.Run("alertStatus filter", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/stats?alertStatus=open", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusOK, doRequest(r, req).Code)
	})

	t.Run("alertSource filter", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/stats?alertSource=wazuh", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusOK, doRequest(r, req).Code)
	})

	t.Run("alertTag filter", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/stats?alertTag=phishing", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusOK, doRequest(r, req).Code)
	})

	t.Run("assignedAnalystId filter -- valid uuid", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/stats?assignedAnalystId="+actorID.String(), nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusOK, doRequest(r, req).Code)
	})

	t.Run("assignedAnalystId filter -- malformed uuid is treated as absent, not an error", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/stats?assignedAnalystId=not-a-uuid", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusOK, doRequest(r, req).Code)
	})

	t.Run("incidentTag filter", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/stats?incidentTag=phishing", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusOK, doRequest(r, req).Code)
	})

	t.Run("commanderId filter -- valid uuid", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/stats?commanderId="+actorID.String(), nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusOK, doRequest(r, req).Code)
	})

	t.Run("since and until both set, valid range", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/stats?since=2000-01-01T00:00:00Z&until=2100-01-01T00:00:00Z", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusOK, doRequest(r, req).Code)
	})

	t.Run("malformed since/until are treated as absent, not an error", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/stats?since=not-a-date&until=also-not-a-date", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusOK, doRequest(r, req).Code)
	})
}

func TestDashboardHandlers_Activity_SinceUntil(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	tagSvc := service.NewTagService(pool, repository.NewTagRepository(), repository.NewAdminAuditEventRepository())
	alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), tagSvc, repository.NewPlaybookRepository())
	incSvc := service.NewIncidentService(pool, repository.NewIncidentRepository(), tagSvc, repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository(), repository.NewAdminAuditEventRepository()))
	dashSvc := service.NewDashboardService(pool, repository.NewDashboardRepository(), alertSvc, incSvc)
	h := handlers.NewDashboardHandlers(dashSvc)
	r := newRouter(h.Routes)

	t.Run("valid since/until narrows the feed", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/activity?since=2000-01-01T00:00:00Z&until=2100-01-01T00:00:00Z&limit=5", nil), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusOK, doRequest(r, req).Code)
	})

	t.Run("malformed since is treated as absent, not an error", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/activity?since=not-a-date", nil), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusOK, doRequest(r, req).Code)
	})
}

func TestDashboardHandlers_Followup_SinceUntil(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	tagSvc := service.NewTagService(pool, repository.NewTagRepository(), repository.NewAdminAuditEventRepository())
	alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), tagSvc, repository.NewPlaybookRepository())
	incSvc := service.NewIncidentService(pool, repository.NewIncidentRepository(), tagSvc, repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository(), repository.NewAdminAuditEventRepository()))
	dashSvc := service.NewDashboardService(pool, repository.NewDashboardRepository(), alertSvc, incSvc)
	h := handlers.NewDashboardHandlers(dashSvc)
	r := newRouter(h.FollowupRoutes)

	req := withClaims(httptest.NewRequest("GET", "/?since=2000-01-01T00:00:00Z&until=2100-01-01T00:00:00Z", nil), tenantID, uuid.New(), nil)
	assert.Equal(t, http.StatusOK, doRequest(r, req).Code)
}

func TestDashboardHandlers_MissingTenantContext(t *testing.T) {
	h := handlers.NewDashboardHandlers(nil)

	t.Run("stats", func(t *testing.T) {
		r := newRouter(h.Routes)
		req := httptest.NewRequest("GET", "/stats", nil)
		assert.Equal(t, http.StatusUnauthorized, doRequest(r, req).Code)
	})

	t.Run("activity", func(t *testing.T) {
		r := newRouter(h.Routes)
		req := httptest.NewRequest("GET", "/activity", nil)
		assert.Equal(t, http.StatusUnauthorized, doRequest(r, req).Code)
	})

	t.Run("followup", func(t *testing.T) {
		r := newRouter(h.FollowupRoutes)
		req := httptest.NewRequest("GET", "/", nil)
		assert.Equal(t, http.StatusUnauthorized, doRequest(r, req).Code)
	})
}
