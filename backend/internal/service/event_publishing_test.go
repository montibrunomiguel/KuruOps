package service_test

import (
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/testutil"
)

type recordedEvent struct {
	tenantID  uuid.UUID
	eventType string
	payload   map[string]any
}

// eventRecorder is a thread-safe stand-in for events.Broadcaster.Publish,
// used to assert AlertService/IncidentService's EnableEventPublishing hook
// fires at the right moments with the right tenant/action -- without
// pulling in the real events package, since SSE fan-out itself is already
// covered by internal/events' own tests and
// httpserver/handlers.EventsHandlers' tests.
type eventRecorder struct {
	mu     sync.Mutex
	events []recordedEvent
}

func (r *eventRecorder) publish(tenantID uuid.UUID, eventType string, payload any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, recordedEvent{tenantID, eventType, payload.(map[string]any)})
}

func (r *eventRecorder) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = nil
}

func (r *eventRecorder) actions() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.events))
	for i, e := range r.events {
		out[i] = e.payload["action"].(string)
	}
	return out
}

func TestAlertService_PublishesEventsOnKeyActions(t *testing.T) {
	_, alertSvc, _ := newAlertServices(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)
	rec := &eventRecorder{}
	alertSvc.EnableEventPublishing(rec.publish)

	alert, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{
		Title: "t", Source: "wazuh", Severity: domain.SeverityHigh, Payload: testPayload,
	})
	require.NoError(t, err)
	require.NoError(t, alertSvc.ChangeStatus(t.Context(), tenantID, alert.ID, actorID, domain.AlertStatusInvestigating, nil))
	require.NoError(t, alertSvc.OverrideSeverity(t.Context(), tenantID, alert.ID, actorID, domain.SeverityCritical, nil))

	assert.Equal(t, []string{"received", "status_changed", "severity_changed"}, rec.actions())
	for _, e := range rec.events {
		assert.Equal(t, tenantID, e.tenantID)
		assert.Equal(t, "alert", e.eventType)
		assert.Equal(t, alert.ID, e.payload["id"])
	}
}

func TestAlertService_OverrideSeverity_NoOpDoesNotPublish(t *testing.T) {
	_, alertSvc, _ := newAlertServices(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)
	rec := &eventRecorder{}
	alertSvc.EnableEventPublishing(rec.publish)

	alert, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{
		Title: "t", Source: "wazuh", Severity: domain.SeverityHigh, Payload: testPayload,
	})
	require.NoError(t, err)
	rec.reset()

	// Same severity it already has -- OverrideSeverity treats this as a
	// no-op (see its early return), so nothing should publish.
	require.NoError(t, alertSvc.OverrideSeverity(t.Context(), tenantID, alert.ID, actorID, domain.SeverityHigh, nil))
	assert.Empty(t, rec.actions())
}

func TestIncidentService_PublishesEventsOnKeyActions(t *testing.T) {
	_, incSvc, _ := newIncidentServices(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)
	rec := &eventRecorder{}
	incSvc.EnableEventPublishing(rec.publish)

	inc, err := incSvc.Create(t.Context(), tenantID, actorID, domain.CreateIncidentInput{
		Title: "Ransomware suspected", Severity: domain.SeverityCritical, Priority: domain.PriorityP1,
	})
	require.NoError(t, err)
	require.NoError(t, incSvc.ChangePhase(t.Context(), tenantID, inc.ID, actorID, domain.PhaseContainment, nil))

	assert.Equal(t, []string{"created", "phase_changed"}, rec.actions())
	for _, e := range rec.events {
		assert.Equal(t, tenantID, e.tenantID)
		assert.Equal(t, "incident", e.eventType)
		assert.Equal(t, inc.ID, e.payload["id"])
	}
}

func TestIncidentService_ChangePhase_NoOpDoesNotPublish(t *testing.T) {
	_, incSvc, _ := newIncidentServices(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)
	rec := &eventRecorder{}
	incSvc.EnableEventPublishing(rec.publish)

	inc, err := incSvc.Create(t.Context(), tenantID, actorID, domain.CreateIncidentInput{
		Title: "t", Severity: domain.SeverityHigh, Priority: domain.PriorityP2,
	})
	require.NoError(t, err)
	rec.reset()

	// Same phase it's already in ('new') -- ChangePhase treats this as a
	// no-op (see its early return), so nothing should publish.
	require.NoError(t, incSvc.ChangePhase(t.Context(), tenantID, inc.ID, actorID, domain.PhaseNew, nil))
	assert.Empty(t, rec.actions())
}
