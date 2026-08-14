package service_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func TestPostmortemService_Generate_NotFound(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	store := secrets.NewEnvStore()

	incidentRepo := repository.NewIncidentRepository()
	incSvc := service.NewIncidentService(pool, incidentRepo, service.NewTagService(pool, repository.NewTagRepository()), repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository()))
	aiSvc, _ := newAIAnalysisService(pool, store)
	pmSvc := service.NewPostmortemService(incSvc, aiSvc)

	doc, found, err := pmSvc.Generate(t.Context(), tenantID, uuid.New(), nil)
	require.NoError(t, err)
	assert.False(t, found)
	assert.Empty(t, doc)
}

// TestPostmortemService_Generate_WithoutAIProvider confirms the hybrid
// design's non-AI half: the structured template (timeline, roles, tags,
// SLA, linked alerts, team notes) is always complete on its own, and the
// "Executive Summary (AI-generated)" section is simply absent -- not an
// error -- when the tenant has no LLM provider configured.
func TestPostmortemService_Generate_WithoutAIProvider(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)
	store := secrets.NewEnvStore()

	incidentRepo := repository.NewIncidentRepository()
	tagSvc := service.NewTagService(pool, repository.NewTagRepository())
	incSvc := service.NewIncidentService(pool, incidentRepo, tagSvc, repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository()))
	alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), tagSvc, repository.NewPlaybookRepository())
	aiSvc, _ := newAIAnalysisService(pool, store)
	pmSvc := service.NewPostmortemService(incSvc, aiSvc)

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

	// CorrectPhaseTimestamp exercises renderTimeline's "Corrected from"
	// branch; Close jumps straight to post_incident and sets ClosedAt,
	// giving the timeline a second entry (so the first entry's duration is
	// bounded by a next entry, not left "ongoing") and exercising
	// formatDuration/slaOutcome along the way.
	require.NoError(t, incSvc.CorrectPhaseTimestamp(t.Context(), tenantID, inc.ID, actorID, domain.PhaseNew, inc.OpenedAt.Add(-time.Hour), "backdated to when detection actually started"))
	require.NoError(t, incSvc.Close(t.Context(), tenantID, inc.ID, actorID, nil))

	doc, found, err := pmSvc.Generate(t.Context(), tenantID, inc.ID, nil)
	require.NoError(t, err)
	require.True(t, found)

	assert.Contains(t, doc, "# Postmortem: Ransomware suspected")
	assert.Contains(t, doc, "Corrected from")
	assert.Contains(t, doc, "backdated to when detection actually started")
	assert.Contains(t, doc, "duration")
	assert.Contains(t, doc, "Suspicious SMB traffic")
	assert.NotContains(t, doc, "Executive Summary (AI-generated)", "no LLM provider configured -- the AI section must be silently omitted")
	assert.Contains(t, doc, "## Phase Timeline")
	assert.Contains(t, doc, "New")
	assert.Contains(t, doc, "## Team Roles")
	assert.Contains(t, doc, "## Linked Alerts")
	assert.Contains(t, doc, "## Team Notes")
	assert.Contains(t, doc, "Analyst One")
	assert.Contains(t, doc, "Contained the affected shares.")
	assert.Contains(t, doc, "ransomware")
}

// TestPostmortemService_Generate_WithAIProvider confirms the other half of
// the hybrid design: once a tenant has an LLM provider configured, the
// executive summary section is prepended using a real (fake HTTP server)
// completion call end to end.
func TestPostmortemService_Generate_WithAIProvider(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)
	store := secrets.NewEnvStore()

	incidentRepo := repository.NewIncidentRepository()
	tagSvc := service.NewTagService(pool, repository.NewTagRepository())
	incSvc := service.NewIncidentService(pool, incidentRepo, tagSvc, repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository()))
	aiSvc, _ := newAIAnalysisService(pool, store)
	pmSvc := service.NewPostmortemService(incSvc, aiSvc)
	llmSvc := service.NewLLMProviderService(pool, repository.NewLLMProviderRepository(), store)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Root cause: a phished credential was reused against an exposed RDP host."}}]}`))
	}))
	defer srv.Close()

	provider, err := llmSvc.Create(t.Context(), tenantID, actorID, service.LLMProviderSaveInput{
		Name: "Test Provider", Kind: "openai_compatible", BaseURL: &srv.URL, Model: "gpt-4o", APIKey: "sk-test",
	})
	require.NoError(t, err)
	require.NoError(t, llmSvc.SetDefault(t.Context(), tenantID, provider.ID))

	inc, err := incSvc.Create(t.Context(), tenantID, actorID, domain.CreateIncidentInput{
		Title: "Ransomware suspected", Severity: domain.SeverityCritical, Priority: domain.PriorityP1,
	})
	require.NoError(t, err)

	doc, found, err := pmSvc.Generate(t.Context(), tenantID, inc.ID, nil)
	require.NoError(t, err)
	require.True(t, found)

	assert.Contains(t, doc, "## Executive Summary (AI-generated)")
	assert.Contains(t, doc, "Root cause: a phished credential was reused against an exposed RDP host.")
}

// TestPostmortemService_Generate_OutOfScopeTag confirms Generate applies
// the same allowedTags visibility rule every other incident read does,
// same "404, indistinguishable from not found" contract as
// IncidentService.Get.
func TestPostmortemService_Generate_OutOfScopeTag(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)
	store := secrets.NewEnvStore()

	incidentRepo := repository.NewIncidentRepository()
	tagSvc := service.NewTagService(pool, repository.NewTagRepository())
	incSvc := service.NewIncidentService(pool, incidentRepo, tagSvc, repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository()))
	aiSvc, _ := newAIAnalysisService(pool, store)
	pmSvc := service.NewPostmortemService(incSvc, aiSvc)

	inc, err := incSvc.Create(t.Context(), tenantID, actorID, domain.CreateIncidentInput{
		Title: "Tag-restricted incident", Severity: domain.SeverityHigh, Priority: domain.PriorityP2,
	})
	require.NoError(t, err)

	doc, found, err := pmSvc.Generate(t.Context(), tenantID, inc.ID, []string{"unrelated-tag"})
	require.NoError(t, err)
	assert.False(t, found)
	assert.Empty(t, doc)
}
