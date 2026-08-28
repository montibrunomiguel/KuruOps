package service_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func TestIncidentReportService_GeneratePDF_NotFound(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)

	incidentRepo := repository.NewIncidentRepository()
	incSvc := service.NewIncidentService(pool, incidentRepo, service.NewTagService(pool, repository.NewTagRepository(), repository.NewAdminAuditEventRepository()), repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository(), repository.NewAdminAuditEventRepository()))
	reportSvc := service.NewIncidentReportService(incSvc)

	pdfBytes, found, err := reportSvc.GeneratePDF(t.Context(), tenantID, uuid.New(), nil)
	require.NoError(t, err)
	assert.False(t, found)
	assert.Empty(t, pdfBytes)
}

// TestIncidentReportService_GeneratePDF_FullRecord mirrors
// TestPostmortemService_Generate_WithoutAIProvider's fixture (same
// timeline-correction, role, linked-alert, and comment setup) since both
// services aggregate the exact same incident record -- just checks the
// rendered PDF's raw bytes for the same substrings that test checks for in
// the Markdown, relying on GeneratePDF's SetCompression(false) to keep the
// content stream text-searchable without a PDF parser.
func TestIncidentReportService_GeneratePDF_FullRecord(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)

	incidentRepo := repository.NewIncidentRepository()
	tagSvc := service.NewTagService(pool, repository.NewTagRepository(), repository.NewAdminAuditEventRepository())
	incSvc := service.NewIncidentService(pool, incidentRepo, tagSvc, repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository(), repository.NewAdminAuditEventRepository()))
	alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), tagSvc, repository.NewPlaybookRepository())
	reportSvc := service.NewIncidentReportService(incSvc)

	inc, err := incSvc.Create(t.Context(), tenantID, actorID, domain.CreateIncidentInput{
		Title: "Ransomware suspected", Description: "Encrypted file shares detected on finance servers.",
		Severity: domain.SeverityCritical, Priority: domain.PriorityP1,
	})
	require.NoError(t, err)
	_, err = tagSvc.Create(t.Context(), tenantID, actorID, "ransomware", nil)
	require.NoError(t, err)
	require.NoError(t, incSvc.UpdateTags(t.Context(), tenantID, inc.ID, actorID, []string{"ransomware"}, nil))
	require.NoError(t, incSvc.SetRole(t.Context(), tenantID, inc.ID, actorID, domain.RoleCommander, []uuid.UUID{actorID}, nil))
	_, err = incSvc.AddComment(t.Context(), tenantID, inc.ID, actorID, "Analyst One", "Contained the affected shares.", nil)
	require.NoError(t, err)

	alert, _, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{
		Title: "Suspicious SMB traffic", Source: "test", Severity: domain.SeverityHigh, Payload: testPayload,
	}, nil, 0)
	require.NoError(t, err)
	require.NoError(t, incSvc.LinkAlert(t.Context(), tenantID, inc.ID, alert.ID, actorID))

	require.NoError(t, incSvc.CorrectPhaseTimestamp(t.Context(), tenantID, inc.ID, actorID, domain.PhaseNew, inc.OpenedAt.Add(-time.Hour), "backdated to when detection actually started"))
	require.NoError(t, incSvc.Close(t.Context(), tenantID, inc.ID, actorID, nil))

	pdfBytes, found, err := reportSvc.GeneratePDF(t.Context(), tenantID, inc.ID, nil)
	require.NoError(t, err)
	require.True(t, found)
	require.NotEmpty(t, pdfBytes)

	assert.True(t, len(pdfBytes) > 4 && string(pdfBytes[:5]) == "%PDF-", "must be a well-formed PDF")

	doc := string(pdfBytes)
	assert.Contains(t, doc, "Ransomware suspected")
	assert.Contains(t, doc, "Encrypted file shares detected on finance servers.")
	assert.Contains(t, doc, "Corrected from")
	assert.Contains(t, doc, "backdated to when detection actually started")
	assert.Contains(t, doc, "duration")
	assert.Contains(t, doc, "Suspicious SMB traffic")
	assert.Contains(t, doc, "Overview")
	assert.Contains(t, doc, "Phase Timeline")
	assert.Contains(t, doc, "Team Roles")
	assert.Contains(t, doc, "Linked Alerts")
	assert.Contains(t, doc, "Team Notes")
	assert.Contains(t, doc, "Analyst One")
	assert.Contains(t, doc, "Contained the affected shares.")
	assert.Contains(t, doc, "ransomware")
}

// TestIncidentReportService_GeneratePDF_MinimalRecord confirms the report
// still renders cleanly for a bare-minimum incident -- no roles, no linked
// alerts, no comments, only the phase it was created into -- showing
// "None." for the sections that would otherwise be empty (see
// renderPDFLinkedAlerts/renderPDFTeamNotes's doc comments for why those
// always get a header printed, unlike the Markdown postmortem).
func TestIncidentReportService_GeneratePDF_MinimalRecord(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)

	incidentRepo := repository.NewIncidentRepository()
	tagSvc := service.NewTagService(pool, repository.NewTagRepository(), repository.NewAdminAuditEventRepository())
	incSvc := service.NewIncidentService(pool, incidentRepo, tagSvc, repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository(), repository.NewAdminAuditEventRepository()))
	reportSvc := service.NewIncidentReportService(incSvc)

	inc, err := incSvc.Create(t.Context(), tenantID, actorID, domain.CreateIncidentInput{
		Title: "Bare incident", Severity: domain.SeverityLow, Priority: domain.PriorityP4,
	})
	require.NoError(t, err)

	pdfBytes, found, err := reportSvc.GeneratePDF(t.Context(), tenantID, inc.ID, nil)
	require.NoError(t, err)
	require.True(t, found)

	doc := string(pdfBytes)
	assert.Contains(t, doc, "Bare incident")
	assert.Contains(t, doc, "Linked Alerts")
	assert.Contains(t, doc, "Team Notes")
	assert.Contains(t, doc, "None.")
	assert.NotContains(t, doc, "Team Roles", "no roles assigned -- that section must be omitted entirely, not printed empty")
}

// TestIncidentReportService_GeneratePDF_OutOfScopeTag confirms GeneratePDF
// applies the same allowedTags visibility rule every other incident read
// does -- same "not found, indistinguishable from missing" contract as
// IncidentService.Get/PostmortemService.Generate.
func TestIncidentReportService_GeneratePDF_OutOfScopeTag(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)

	incidentRepo := repository.NewIncidentRepository()
	tagSvc := service.NewTagService(pool, repository.NewTagRepository(), repository.NewAdminAuditEventRepository())
	incSvc := service.NewIncidentService(pool, incidentRepo, tagSvc, repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository(), repository.NewAdminAuditEventRepository()))
	reportSvc := service.NewIncidentReportService(incSvc)

	inc, err := incSvc.Create(t.Context(), tenantID, actorID, domain.CreateIncidentInput{
		Title: "Tag-restricted incident", Severity: domain.SeverityHigh, Priority: domain.PriorityP2,
	})
	require.NoError(t, err)

	pdfBytes, found, err := reportSvc.GeneratePDF(t.Context(), tenantID, inc.ID, []string{"unrelated-tag"})
	require.NoError(t, err)
	assert.False(t, found)
	assert.Empty(t, pdfBytes)
}
