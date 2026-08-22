package service_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func TestAuditExportService_ExportCEF(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	alertRepo := repository.NewAlertRepository()
	svc := service.NewAuditExportService(pool, repository.NewAuditRepository())

	t.Run("a tenant with no history exports zero lines and no next cursor", func(t *testing.T) {
		lines, next, err := svc.ExportCEF(t.Context(), tenantID, nil, 100)
		require.NoError(t, err)
		assert.Empty(t, lines)
		assert.Nil(t, next)
	})

	tx := testutil.BeginTx(t, pool, tenantID)
	a := &domain.Alert{
		TenantID: tenantID, Title: "Suspicious login", Source: "test",
		Severity: domain.SeverityHigh, OriginalSeverity: domain.SeverityHigh, Status: domain.AlertStatusOpen,
		Tags: []string{}, Payload: json.RawMessage(`{}`), ReceivedAt: time.Now(),
	}
	require.NoError(t, alertRepo.Insert(t.Context(), tx, a))
	require.NoError(t, alertRepo.InsertEvent(t.Context(), tx, &domain.AlertEvent{
		AlertID: a.ID, TenantID: tenantID, EventType: domain.AlertEventReceived,
		ActorType: domain.ActorSystem, Data: json.RawMessage(`{}`),
	}))
	require.NoError(t, tx.Commit(t.Context()), "ExportCEF runs its own WithTenant tx, so the fixture must be committed first")

	t.Run("formats the committed event as a CEF line", func(t *testing.T) {
		lines, next, err := svc.ExportCEF(t.Context(), tenantID, nil, 100)
		require.NoError(t, err)
		require.Len(t, lines, 1)
		assert.True(t, strings.HasPrefix(lines[0], "CEF:0|ArgusOps|ArgusOps|1.0|alert.received|"))
		assert.Nil(t, next, "a page shorter than the limit has no next cursor")
	})

	t.Run("a full page returns a next cursor for resuming the export", func(t *testing.T) {
		lines, next, err := svc.ExportCEF(t.Context(), tenantID, nil, 1)
		require.NoError(t, err)
		require.Len(t, lines, 1)
		require.NotNil(t, next, "page length == limit signals more may exist")

		lines2, next2, err := svc.ExportCEF(t.Context(), tenantID, next, 1)
		require.NoError(t, err)
		assert.Empty(t, lines2, "nothing left after the only event")
		assert.Nil(t, next2)
	})
}

// TestAuditExportService_ExportJSON only covers what's actually different
// from ExportCEF (the domain.AuditEvent values returned instead of
// formatted CEF lines) -- pagination and tenant isolation are the shared
// exportEvents helper both methods call, already covered above.
func TestAuditExportService_ExportJSON(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	alertRepo := repository.NewAlertRepository()
	svc := service.NewAuditExportService(pool, repository.NewAuditRepository())

	tx := testutil.BeginTx(t, pool, tenantID)
	a := &domain.Alert{
		TenantID: tenantID, Title: "Suspicious login", Source: "test",
		Severity: domain.SeverityHigh, OriginalSeverity: domain.SeverityHigh, Status: domain.AlertStatusOpen,
		Tags: []string{}, Payload: json.RawMessage(`{}`), ReceivedAt: time.Now(),
	}
	require.NoError(t, alertRepo.Insert(t.Context(), tx, a))
	require.NoError(t, alertRepo.InsertEvent(t.Context(), tx, &domain.AlertEvent{
		AlertID: a.ID, TenantID: tenantID, EventType: domain.AlertEventReceived,
		ActorType: domain.ActorSystem, Data: json.RawMessage(`{}`),
	}))
	require.NoError(t, tx.Commit(t.Context()))

	events, next, err := svc.ExportJSON(t.Context(), tenantID, nil, 100)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "alert", events[0].Kind)
	assert.Equal(t, a.ID, events[0].ContextID)
	assert.Equal(t, string(domain.AlertEventReceived), events[0].EventType)
	assert.Nil(t, next)
}

func TestAuditExportService_ExportCEF_TenantIsolation(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantA := testutil.NewTenant(t)
	tenantB := testutil.NewTenant(t)
	alertRepo := repository.NewAlertRepository()
	svc := service.NewAuditExportService(pool, repository.NewAuditRepository())

	tx := testutil.BeginTx(t, pool, tenantA)
	a := &domain.Alert{
		TenantID: tenantA, Title: "Suspicious login", Source: "test",
		Severity: domain.SeverityHigh, OriginalSeverity: domain.SeverityHigh, Status: domain.AlertStatusOpen,
		Tags: []string{}, Payload: json.RawMessage(`{}`), ReceivedAt: time.Now(),
	}
	require.NoError(t, alertRepo.Insert(t.Context(), tx, a))
	require.NoError(t, alertRepo.InsertEvent(t.Context(), tx, &domain.AlertEvent{
		AlertID: a.ID, TenantID: tenantA, EventType: domain.AlertEventReceived,
		ActorType: domain.ActorSystem, Data: json.RawMessage(`{}`),
	}))
	require.NoError(t, tx.Commit(t.Context()))

	lines, _, err := svc.ExportCEF(t.Context(), tenantB, nil, 100)
	require.NoError(t, err)
	assert.Empty(t, lines, "tenant B must not see tenant A's audit history")
}
