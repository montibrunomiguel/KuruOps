package service_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func newDashboardService(t *testing.T) *service.DashboardService {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	tagSvc := service.NewTagService(pool, repository.NewTagRepository())
	alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), tagSvc)
	incSvc := service.NewIncidentService(pool, repository.NewIncidentRepository(), tagSvc, repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository()))
	return service.NewDashboardService(pool, repository.NewDashboardRepository(), alertSvc, incSvc)
}

func TestDashboardService_Stats(t *testing.T) {
	svc := newDashboardService(t)
	tenantID := testutil.NewTenant(t)

	stats, err := svc.Stats(t.Context(), tenantID, repository.StatsFilter{})
	require.NoError(t, err)
	assert.Zero(t, stats.OpenAlerts)
}

// TestDashboardService_Stats_Filtered guards the Alerts-tab and
// Incidents-tab filter bars (see repository.StatsFilter) all the way
// through the service layer.
func TestDashboardService_Stats_Filtered(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	tagSvc := service.NewTagService(pool, repository.NewTagRepository())
	alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), tagSvc)
	incSvc := service.NewIncidentService(pool, repository.NewIncidentRepository(), tagSvc, repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository()))
	dashSvc := service.NewDashboardService(pool, repository.NewDashboardRepository(), alertSvc, incSvc)
	endpointID := testutil.NewWebhookEndpoint(t, tenantID)

	_, err := alertSvc.Ingest(t.Context(), tenantID, endpointID, domain.Alert{
		Title: "a", Source: "s", Severity: domain.SeverityCritical, Payload: testPayload,
	})
	require.NoError(t, err)
	_, err = alertSvc.Ingest(t.Context(), tenantID, endpointID, domain.Alert{
		Title: "b", Source: "s", Severity: domain.SeverityLow, Payload: testPayload,
	})
	require.NoError(t, err)

	critical := domain.SeverityCritical
	stats, err := dashSvc.Stats(t.Context(), tenantID, repository.StatsFilter{AlertSeverity: &critical})
	require.NoError(t, err)
	assert.Equal(t, 1, stats.OpenAlerts, "only the critical alert matches the filter")
	assert.Equal(t, 1, stats.CriticalAlerts)
}

// TestDashboardService_Stats_AllowedTagsScoping confirms the allowedTags
// scoping fix in DashboardRepository.Stats survives the service layer too
// (StatsFilter.AllowedTags passed straight through by the caller).
func TestDashboardService_Stats_AllowedTagsScoping(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	tagSvc := service.NewTagService(pool, repository.NewTagRepository())
	alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), tagSvc)
	incSvc := service.NewIncidentService(pool, repository.NewIncidentRepository(), tagSvc, repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository()))
	dashSvc := service.NewDashboardService(pool, repository.NewDashboardRepository(), alertSvc, incSvc)
	endpointID := testutil.NewWebhookEndpoint(t, tenantID)

	_, err := alertSvc.Ingest(t.Context(), tenantID, endpointID, domain.Alert{
		Title: "in scope", Source: "s", Severity: domain.SeverityCritical, Tags: []string{"ifood"}, Payload: testPayload,
	})
	require.NoError(t, err)
	_, err = alertSvc.Ingest(t.Context(), tenantID, endpointID, domain.Alert{
		Title: "out of scope", Source: "s", Severity: domain.SeverityCritical, Tags: []string{"other-company"}, Payload: testPayload,
	})
	require.NoError(t, err)

	stats, err := dashSvc.Stats(t.Context(), tenantID, repository.StatsFilter{AllowedTags: []string{"ifood"}})
	require.NoError(t, err)
	assert.Equal(t, 1, stats.OpenAlerts, "only the ifood-tagged alert is in scope")
}

// TestDashboardService_Activity_AllowedTagsScoping mirrors the Stats
// version above for the Recent Activity feed.
func TestDashboardService_Activity_AllowedTagsScoping(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	tagSvc := service.NewTagService(pool, repository.NewTagRepository())
	alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), tagSvc)
	incSvc := service.NewIncidentService(pool, repository.NewIncidentRepository(), tagSvc, repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository()))
	dashSvc := service.NewDashboardService(pool, repository.NewDashboardRepository(), alertSvc, incSvc)
	endpointID := testutil.NewWebhookEndpoint(t, tenantID)

	_, err := alertSvc.Ingest(t.Context(), tenantID, endpointID, domain.Alert{
		Title: "in scope", Source: "s", Severity: domain.SeverityHigh, Tags: []string{"ifood"}, Payload: testPayload,
	})
	require.NoError(t, err)
	_, err = alertSvc.Ingest(t.Context(), tenantID, endpointID, domain.Alert{
		Title: "out of scope", Source: "s", Severity: domain.SeverityHigh, Tags: []string{"other-company"}, Payload: testPayload,
	})
	require.NoError(t, err)

	events, err := dashSvc.Activity(t.Context(), tenantID, 0, "", []string{"ifood"}, nil, nil)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "in scope", events[0].ContextTitle)
}

func TestDashboardService_Activity(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	tagSvc := service.NewTagService(pool, repository.NewTagRepository())
	alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), tagSvc)
	incSvc := service.NewIncidentService(pool, repository.NewIncidentRepository(), tagSvc, repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository()))
	dashSvc := service.NewDashboardService(pool, repository.NewDashboardRepository(), alertSvc, incSvc)

	_, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{Title: "a", Source: "s", Severity: domain.SeverityHigh, Payload: testPayload})
	require.NoError(t, err)

	events, err := dashSvc.Activity(t.Context(), tenantID, 0, "", nil, nil, nil)
	require.NoError(t, err)
	require.Len(t, events, 1, "a non-positive limit defaults to 20, not zero results")
	assert.Equal(t, "alert", events[0].Kind)
}

func TestDashboardService_Activity_KindFilter(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	tagSvc := service.NewTagService(pool, repository.NewTagRepository())
	alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), tagSvc)
	incSvc := service.NewIncidentService(pool, repository.NewIncidentRepository(), tagSvc, repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository()))
	dashSvc := service.NewDashboardService(pool, repository.NewDashboardRepository(), alertSvc, incSvc)

	_, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{Title: "a", Source: "s", Severity: domain.SeverityHigh, Payload: testPayload})
	require.NoError(t, err)

	incidentEvents, err := dashSvc.Activity(t.Context(), tenantID, 0, "incident", nil, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, incidentEvents, "kind=incident must not surface the alert we just ingested")

	alertEvents, err := dashSvc.Activity(t.Context(), tenantID, 0, "alert", nil, nil, nil)
	require.NoError(t, err)
	require.Len(t, alertEvents, 1)
	assert.Equal(t, "alert", alertEvents[0].Kind)
}

func TestDashboardService_Followup(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)
	tagSvc := service.NewTagService(pool, repository.NewTagRepository())
	alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), tagSvc)
	incSvc := service.NewIncidentService(pool, repository.NewIncidentRepository(), tagSvc, repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository()))
	dashSvc := service.NewDashboardService(pool, repository.NewDashboardRepository(), alertSvc, incSvc)

	// An escalated alert, an investigating one, and a still-open (untriaged)
	// one all belong in Follow-up; a closed one does not.
	escalated, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{Title: "a", Source: "s", Severity: domain.SeverityHigh, Payload: testPayload})
	require.NoError(t, err)
	require.NoError(t, alertSvc.ChangeStatus(t.Context(), tenantID, escalated.ID, actorID, domain.AlertStatusEscalated, nil))

	investigating, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{Title: "b", Source: "s", Severity: domain.SeverityLow, Payload: testPayload})
	require.NoError(t, err)
	require.NoError(t, alertSvc.ChangeStatus(t.Context(), tenantID, investigating.ID, actorID, domain.AlertStatusInvestigating, nil))

	open, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{Title: "c", Source: "s", Severity: domain.SeverityLow, Payload: testPayload})
	require.NoError(t, err)

	view, err := dashSvc.Followup(t.Context(), tenantID, nil, nil, nil)
	require.NoError(t, err)
	assert.Len(t, view.Alerts, 3, "open, escalated, and investigating alerts all appear in follow-up")

	ids := []uuid.UUID{view.Alerts[0].ID, view.Alerts[1].ID, view.Alerts[2].ID}
	assert.Contains(t, ids, escalated.ID)
	assert.Contains(t, ids, investigating.ID)
	assert.Contains(t, ids, open.ID, "a freshly-received untriaged alert needs follow-up too")

	t.Run("respects allowedTags scoping", func(t *testing.T) {
		view, err := dashSvc.Followup(t.Context(), tenantID, []string{"unrelated-tag"}, nil, nil)
		require.NoError(t, err)
		assert.Empty(t, view.Alerts)
	})
}
