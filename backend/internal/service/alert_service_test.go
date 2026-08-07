package service_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

// testPayload is a stand-in for the raw webhook body every real alert
// carries -- alerts.payload is NOT NULL, so any fixture built directly (as
// opposed to via a real ingest handler) must set something.
var testPayload = json.RawMessage(`{}`)

func newAlertServices(t *testing.T) (*db.Pool, *service.AlertService, *service.TagService) {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	tagSvc := service.NewTagService(pool, repository.NewTagRepository())
	alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), tagSvc)
	return pool, alertSvc, tagSvc
}

func TestAlertService_Ingest(t *testing.T) {
	_, alertSvc, tagSvc := newAlertServices(t)
	tenantID := testutil.NewTenant(t)
	endpointID := testutil.NewWebhookEndpoint(t, tenantID)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)

	_, err := tagSvc.Create(t.Context(), tenantID, actorID, "phishing", nil)
	require.NoError(t, err)

	t.Run("always starts open with no classification, regardless of input", func(t *testing.T) {
		alert, err := alertSvc.Ingest(t.Context(), tenantID, endpointID, domain.Alert{
			Title: "Suspicious login", Source: "wazuh",
			Severity: domain.SeverityHigh, Status: domain.AlertStatusClosed,
			Tags: []string{"phishing", "unregistered-tag"}, Payload: testPayload,
		})
		require.NoError(t, err)
		assert.Equal(t, domain.AlertStatusOpen, alert.Status)
		assert.Nil(t, alert.Classification)
		assert.Equal(t, domain.SeverityHigh, alert.OriginalSeverity, "OriginalSeverity mirrors the ingested severity for later diffing")
		assert.False(t, alert.ReceivedAt.IsZero())

		t.Run("tags are stored as given -- catalog filtering happens upstream in the ingest handler, not here", func(t *testing.T) {
			assert.Equal(t, []string{"phishing", "unregistered-tag"}, alert.Tags)
		})
	})

	t.Run("nil tags become an empty slice, never a NULL", func(t *testing.T) {
		alert, err := alertSvc.Ingest(t.Context(), tenantID, endpointID, domain.Alert{
			Title: "No tags", Source: "wazuh", Severity: domain.SeverityLow, Payload: testPayload,
		})
		require.NoError(t, err)
		assert.Equal(t, []string{}, alert.Tags)
	})
}

// fakeOnCallResolver is a minimal service.OnCallResolver double -- avoids
// pulling in the real on-call schema (tenant timezone, shifts) just to
// verify Ingest wires the resolved analyst onto the new alert.
type fakeOnCallResolver struct {
	analystID *uuid.UUID
	err       error
}

func (f *fakeOnCallResolver) ResolveCurrentAnalyst(ctx context.Context, tenantID uuid.UUID, now time.Time) (*uuid.UUID, error) {
	return f.analystID, f.err
}

func TestAlertService_Ingest_OnCallAutoAssign(t *testing.T) {
	pool, alertSvc, _ := newAlertServices(t)
	tenantID := testutil.NewTenant(t)
	endpointID := testutil.NewWebhookEndpoint(t, tenantID)
	analystID := testutil.NewUser(t, tenantID, "analyst", nil)

	t.Run("no resolver enabled -- alert stays unassigned", func(t *testing.T) {
		alert, err := alertSvc.Ingest(t.Context(), tenantID, endpointID, domain.Alert{
			Title: "t", Source: "s", Severity: domain.SeverityLow, Payload: testPayload,
		})
		require.NoError(t, err)
		assert.Nil(t, alert.AssignedAnalystID)
	})

	t.Run("resolver enabled with a match -- alert is auto-assigned", func(t *testing.T) {
		svc := service.NewAlertService(pool, repository.NewAlertRepository(), service.NewTagService(pool, repository.NewTagRepository()))
		svc.EnableOnCallAutoAssign(&fakeOnCallResolver{analystID: &analystID})

		alert, err := svc.Ingest(t.Context(), tenantID, endpointID, domain.Alert{
			Title: "t", Source: "s", Severity: domain.SeverityLow, Payload: testPayload,
		})
		require.NoError(t, err)
		require.NotNil(t, alert.AssignedAnalystID)
		assert.Equal(t, analystID, *alert.AssignedAnalystID)
	})

	t.Run("resolver enabled with no match -- alert stays unassigned, not an error", func(t *testing.T) {
		svc := service.NewAlertService(pool, repository.NewAlertRepository(), service.NewTagService(pool, repository.NewTagRepository()))
		svc.EnableOnCallAutoAssign(&fakeOnCallResolver{analystID: nil})

		alert, err := svc.Ingest(t.Context(), tenantID, endpointID, domain.Alert{
			Title: "t", Source: "s", Severity: domain.SeverityLow, Payload: testPayload,
		})
		require.NoError(t, err)
		assert.Nil(t, alert.AssignedAnalystID)
	})
}

func TestAlertService_GetVisibility(t *testing.T) {
	_, alertSvc, _ := newAlertServices(t)
	tenantID := testutil.NewTenant(t)

	alert, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{
		Title: "t", Source: "s", Severity: domain.SeverityLow, Tags: []string{}, Payload: testPayload,
	})
	require.NoError(t, err)

	t.Run("unrestricted (empty allowedTags) sees everything", func(t *testing.T) {
		got, err := alertSvc.Get(t.Context(), tenantID, alert.ID, nil)
		require.NoError(t, err)
		require.NotNil(t, got)
	})

	t.Run("a tag-scoped caller without overlap gets nil, same as not-found", func(t *testing.T) {
		got, err := alertSvc.Get(t.Context(), tenantID, alert.ID, []string{"unrelated-tag"})
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("an unknown id also returns nil, indistinguishable from a scoped-out one", func(t *testing.T) {
		got, err := alertSvc.Get(t.Context(), tenantID, uuid.New(), nil)
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}

func TestAlertService_ChangeStatus(t *testing.T) {
	_, alertSvc, _ := newAlertServices(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)

	t.Run("rejects a direct transition to closed", func(t *testing.T) {
		alert, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{Title: "t", Source: "s", Severity: domain.SeverityLow, Payload: testPayload})
		require.NoError(t, err)
		err = alertSvc.ChangeStatus(t.Context(), tenantID, alert.ID, actorID, domain.AlertStatusClosed, nil)
		assert.ErrorContains(t, err, "use Close")
	})

	t.Run("a normal transition stamps acknowledged_at the first time it leaves open", func(t *testing.T) {
		alert, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{Title: "t", Source: "s", Severity: domain.SeverityLow, Payload: testPayload})
		require.NoError(t, err)
		require.NoError(t, alertSvc.ChangeStatus(t.Context(), tenantID, alert.ID, actorID, domain.AlertStatusInvestigating, nil))

		got, err := alertSvc.Get(t.Context(), tenantID, alert.ID, nil)
		require.NoError(t, err)
		assert.Equal(t, domain.AlertStatusInvestigating, got.Status)
		assert.NotNil(t, got.AcknowledgedAt)
	})

	t.Run("a closed alert's status is read-only", func(t *testing.T) {
		alert, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{Title: "t", Source: "s", Severity: domain.SeverityLow, Payload: testPayload})
		require.NoError(t, err)
		require.NoError(t, alertSvc.Close(t.Context(), tenantID, alert.ID, actorID, domain.CloseAlertInput{
			Classification: domain.ClassificationTruePositive, Comment: "confirmed",
		}, nil))

		err = alertSvc.ChangeStatus(t.Context(), tenantID, alert.ID, actorID, domain.AlertStatusInvestigating, nil)
		assert.ErrorContains(t, err, "closed and its status is read-only")
	})

	t.Run("an out-of-scope alert reads as not found, not forbidden", func(t *testing.T) {
		alert, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{Title: "t", Source: "s", Severity: domain.SeverityLow, Payload: testPayload})
		require.NoError(t, err)
		err = alertSvc.ChangeStatus(t.Context(), tenantID, alert.ID, actorID, domain.AlertStatusInvestigating, []string{"unrelated-tag"})
		assert.ErrorContains(t, err, "not found")
	})
}

func TestAlertService_Close(t *testing.T) {
	_, alertSvc, _ := newAlertServices(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)

	alert, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{Title: "t", Source: "s", Severity: domain.SeverityLow, Payload: testPayload})
	require.NoError(t, err)

	require.NoError(t, alertSvc.Close(t.Context(), tenantID, alert.ID, actorID, domain.CloseAlertInput{
		Classification: domain.ClassificationFalsePositive, Comment: "benign",
	}, nil))

	got, err := alertSvc.Get(t.Context(), tenantID, alert.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, domain.AlertStatusClosed, got.Status)
	require.NotNil(t, got.Classification)
	assert.Equal(t, domain.ClassificationFalsePositive, *got.Classification)

	t.Run("closing twice is rejected", func(t *testing.T) {
		err := alertSvc.Close(t.Context(), tenantID, alert.ID, actorID, domain.CloseAlertInput{
			Classification: domain.ClassificationTruePositive, Comment: "x",
		}, nil)
		assert.ErrorContains(t, err, "already closed")
	})
}

func TestAlertService_OverrideSeverity(t *testing.T) {
	_, alertSvc, _ := newAlertServices(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)

	t.Run("overrides severity without touching original_severity", func(t *testing.T) {
		alert, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{Title: "t", Source: "s", Severity: domain.SeverityLow, Payload: testPayload})
		require.NoError(t, err)

		require.NoError(t, alertSvc.OverrideSeverity(t.Context(), tenantID, alert.ID, actorID, domain.SeverityCritical, nil))

		got, err := alertSvc.Get(t.Context(), tenantID, alert.ID, nil)
		require.NoError(t, err)
		assert.Equal(t, domain.SeverityCritical, got.Severity)
		assert.Equal(t, domain.SeverityLow, got.OriginalSeverity)
	})

	t.Run("a closed alert's severity is read-only", func(t *testing.T) {
		alert, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{Title: "t", Source: "s", Severity: domain.SeverityLow, Payload: testPayload})
		require.NoError(t, err)
		require.NoError(t, alertSvc.Close(t.Context(), tenantID, alert.ID, actorID, domain.CloseAlertInput{
			Classification: domain.ClassificationTruePositive, Comment: "confirmed",
		}, nil))

		err = alertSvc.OverrideSeverity(t.Context(), tenantID, alert.ID, actorID, domain.SeverityCritical, nil)
		assert.ErrorContains(t, err, "closed and its severity is read-only")
	})

	t.Run("an out-of-scope alert reads as not found", func(t *testing.T) {
		alert, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{Title: "t", Source: "s", Severity: domain.SeverityLow, Payload: testPayload})
		require.NoError(t, err)
		err = alertSvc.OverrideSeverity(t.Context(), tenantID, alert.ID, actorID, domain.SeverityCritical, []string{"unrelated-tag"})
		assert.ErrorContains(t, err, "not found")
	})
}

func TestAlertService_Reassign(t *testing.T) {
	_, alertSvc, _ := newAlertServices(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)
	analystID := testutil.NewUser(t, tenantID, "analyst", nil)

	t.Run("assigns then clears the analyst, recording an event each time", func(t *testing.T) {
		alert, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{Title: "t", Source: "s", Severity: domain.SeverityLow, Payload: testPayload})
		require.NoError(t, err)
		assert.Nil(t, alert.AssignedAnalystID)

		require.NoError(t, alertSvc.Reassign(t.Context(), tenantID, alert.ID, actorID, &analystID, nil))
		got, err := alertSvc.Get(t.Context(), tenantID, alert.ID, nil)
		require.NoError(t, err)
		require.NotNil(t, got.AssignedAnalystID)
		assert.Equal(t, analystID, *got.AssignedAnalystID)

		require.NoError(t, alertSvc.Reassign(t.Context(), tenantID, alert.ID, actorID, nil, nil))
		got, err = alertSvc.Get(t.Context(), tenantID, alert.ID, nil)
		require.NoError(t, err)
		assert.Nil(t, got.AssignedAnalystID)
	})

	t.Run("an out-of-scope alert reads as not found", func(t *testing.T) {
		alert, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{Title: "t", Source: "s", Severity: domain.SeverityLow, Payload: testPayload})
		require.NoError(t, err)
		err = alertSvc.Reassign(t.Context(), tenantID, alert.ID, actorID, &analystID, []string{"unrelated-tag"})
		assert.ErrorContains(t, err, "not found")
	})
}

func TestAlertService_LinkAlert(t *testing.T) {
	_, alertSvc, _ := newAlertServices(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)
	endpointID := testutil.NewWebhookEndpoint(t, tenantID)

	newAlert := func(t *testing.T, title string) *domain.Alert {
		a, err := alertSvc.Ingest(t.Context(), tenantID, endpointID, domain.Alert{Title: title, Source: "s", Severity: domain.SeverityLow, Payload: testPayload})
		require.NoError(t, err)
		return a
	}

	t.Run("linking is symmetric -- either side lists the other", func(t *testing.T) {
		a := newAlert(t, "a")
		b := newAlert(t, "b")

		require.NoError(t, alertSvc.LinkAlert(t.Context(), tenantID, a.ID, b.ID, actorID, nil))

		aLinks, err := alertSvc.LinkedAlerts(t.Context(), tenantID, a.ID, nil)
		require.NoError(t, err)
		require.Len(t, aLinks, 1)
		assert.Equal(t, b.ID, aLinks[0].ID)

		bLinks, err := alertSvc.LinkedAlerts(t.Context(), tenantID, b.ID, nil)
		require.NoError(t, err)
		require.Len(t, bLinks, 1)
		assert.Equal(t, a.ID, bLinks[0].ID)
	})

	t.Run("cannot link an alert to itself", func(t *testing.T) {
		a := newAlert(t, "self")
		err := alertSvc.LinkAlert(t.Context(), tenantID, a.ID, a.ID, actorID, nil)
		assert.ErrorContains(t, err, "cannot link an alert to itself")
	})

	t.Run("unlinking removes both directions", func(t *testing.T) {
		a := newAlert(t, "a2")
		b := newAlert(t, "b2")
		require.NoError(t, alertSvc.LinkAlert(t.Context(), tenantID, a.ID, b.ID, actorID, nil))

		require.NoError(t, alertSvc.UnlinkAlert(t.Context(), tenantID, a.ID, b.ID, nil))

		aLinks, err := alertSvc.LinkedAlerts(t.Context(), tenantID, a.ID, nil)
		require.NoError(t, err)
		assert.Empty(t, aLinks)
		bLinks, err := alertSvc.LinkedAlerts(t.Context(), tenantID, b.ID, nil)
		require.NoError(t, err)
		assert.Empty(t, bLinks)
	})

	t.Run("linking to an out-of-scope alert fails as not found", func(t *testing.T) {
		a := newAlert(t, "a3")
		b := newAlert(t, "b3")
		err := alertSvc.LinkAlert(t.Context(), tenantID, a.ID, b.ID, actorID, []string{"unrelated-tag"})
		assert.ErrorContains(t, err, "not found")
	})
}

func TestAlertService_UpdateTags(t *testing.T) {
	_, alertSvc, tagSvc := newAlertServices(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)

	_, err := tagSvc.Create(t.Context(), tenantID, actorID, "vpn", nil)
	require.NoError(t, err)

	alert, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{Title: "t", Source: "s", Severity: domain.SeverityLow, Payload: testPayload})
	require.NoError(t, err)

	t.Run("only catalog tags are attached", func(t *testing.T) {
		require.NoError(t, alertSvc.UpdateTags(t.Context(), tenantID, alert.ID, actorID, []string{"vpn", "not-registered"}, nil))
		got, err := alertSvc.Get(t.Context(), tenantID, alert.ID, nil)
		require.NoError(t, err)
		assert.Equal(t, []string{"vpn"}, got.Tags)
	})

	t.Run("an out-of-scope alert can't have its tags updated", func(t *testing.T) {
		err := alertSvc.UpdateTags(t.Context(), tenantID, alert.ID, actorID, []string{"vpn"}, []string{"unrelated-tag"})
		assert.ErrorContains(t, err, "not found")
	})
}
