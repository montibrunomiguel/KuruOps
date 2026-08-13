package repository_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/testutil"
)

func newAIAnalysisRunFixture(t *testing.T) (*repository.AIAnalysisRunRepository, pgx.Tx, *domain.AIAnalysisRun) {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewAIAnalysisRunRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	run := &domain.AIAnalysisRun{
		TenantID:    tenantID,
		ContextType: "alert",
		ContextID:   uuid.New(),
		Status:      domain.AIAnalysisRunRunning,
		Messages:    json.RawMessage(`[]`),
		Tools:       json.RawMessage(`[]`),
		ToolRoutes:  json.RawMessage(`{}`),
	}
	require.NoError(t, repo.Insert(t.Context(), tx, run))
	return repo, tx, run
}

func TestAIAnalysisRunRepository_InsertGetList(t *testing.T) {
	repo, tx, run := newAIAnalysisRunFixture(t)

	t.Run("get", func(t *testing.T) {
		got, err := repo.Get(t.Context(), tx, run.ID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, domain.AIAnalysisRunRunning, got.Status)
		assert.Equal(t, run.ContextID, got.ContextID)
	})

	t.Run("get unknown id returns nil, nil", func(t *testing.T) {
		got, err := repo.Get(t.Context(), tx, run.ID+999999)
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("ListByContext finds it by context type+id", func(t *testing.T) {
		list, err := repo.ListByContext(t.Context(), tx, run.ContextType, run.ContextID)
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, run.ID, list[0].ID)
	})

	t.Run("ListByContext on an unrelated context returns empty, not nil", func(t *testing.T) {
		list, err := repo.ListByContext(t.Context(), tx, "incident", uuid.New())
		require.NoError(t, err)
		assert.Empty(t, list)
	})
}

func TestAIAnalysisRunRepository_PauseResumeCycle(t *testing.T) {
	repo, tx, run := newAIAnalysisRunFixture(t)

	// pending_tool_call_id has a real FK to ai_tool_calls -- SetPaused needs
	// an actual row, not an arbitrary int.
	mcpRepo := repository.NewMCPServerRepository()
	server := &domain.MCPServer{
		TenantID: run.TenantID, Name: "Test MCP", Transport: "http",
		EndpointOrCommand: "https://mcp.example.com", AllowedTools: []string{"lookup_ip"},
		EnabledFor: []string{"alert"}, SideEffectingTools: []string{},
	}
	require.NoError(t, mcpRepo.Insert(t.Context(), tx, server))
	toolCallRepo := repository.NewAIToolCallRepository()
	toolCall := &domain.AIToolCall{
		TenantID: run.TenantID, MCPServerID: server.ID, ToolName: "lookup_ip",
		ContextType: run.ContextType, ContextID: run.ContextID,
		Args: json.RawMessage(`{}`), Status: domain.ToolCallProposed,
	}
	require.NoError(t, toolCallRepo.Insert(t.Context(), tx, toolCall))
	toolCallID := toolCall.ID

	pausedMessages := json.RawMessage(`[{"role":"assistant"}]`)
	require.NoError(t, repo.SetPaused(t.Context(), tx, run.ID, pausedMessages, toolCallID))

	t.Run("GetPausedByToolCall finds the paused run", func(t *testing.T) {
		got, err := repo.GetPausedByToolCall(t.Context(), tx, toolCallID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, run.ID, got.ID)
		assert.Equal(t, domain.AIAnalysisRunPaused, got.Status)
		require.NotNil(t, got.PendingToolCallID)
		assert.Equal(t, toolCallID, *got.PendingToolCallID)
	})

	t.Run("GetPausedByToolCall on an unrelated call id finds nothing", func(t *testing.T) {
		got, err := repo.GetPausedByToolCall(t.Context(), tx, toolCallID+1)
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	resumedMessages := json.RawMessage(`[{"role":"assistant"},{"role":"tool"}]`)
	require.NoError(t, repo.SetRunning(t.Context(), tx, run.ID, resumedMessages))

	t.Run("SetRunning clears the pending tool call and flips status back", func(t *testing.T) {
		got, err := repo.Get(t.Context(), tx, run.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.AIAnalysisRunRunning, got.Status)
		assert.Nil(t, got.PendingToolCallID)

		// The run is no longer paused on this call id, so it must stop
		// showing up here -- otherwise a second, unrelated approval could
		// resume the same run twice.
		stillPaused, err := repo.GetPausedByToolCall(t.Context(), tx, toolCallID)
		require.NoError(t, err)
		assert.Nil(t, stillPaused)
	})
}

func TestAIAnalysisRunRepository_AppendUserMessage(t *testing.T) {
	repo, tx, run := newAIAnalysisRunFixture(t)

	require.NoError(t, repo.SetCompleted(t.Context(), tx, run.ID, json.RawMessage(`[{"role":"assistant","content":"first"}]`), "first"))

	appended := json.RawMessage(`[{"role":"assistant","content":"first"},{"role":"user","content":"follow-up"}]`)
	require.NoError(t, repo.AppendUserMessage(t.Context(), tx, run.ID, appended))

	got, err := repo.Get(t.Context(), tx, run.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, domain.AIAnalysisRunRunning, got.Status, "continuing a completed run flips it back to running")
	assert.JSONEq(t, string(appended), string(got.Messages))
	assert.Nil(t, got.PendingToolCallID)
}

func TestAIAnalysisRunRepository_SetFailed(t *testing.T) {
	repo, tx, run := newAIAnalysisRunFixture(t)

	require.NoError(t, repo.SetFailed(t.Context(), tx, run.ID, "llm provider timed out"))

	got, err := repo.Get(t.Context(), tx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.AIAnalysisRunFailed, got.Status)
	require.NotNil(t, got.Error)
	assert.Equal(t, "llm provider timed out", *got.Error)
	assert.Nil(t, got.PendingToolCallID)

	// LatestRun must surface the failed status/error, not pretend nothing
	// happened.
	latest, err := repo.LatestRun(t.Context(), tx, run.ContextType, run.ContextID)
	require.NoError(t, err)
	require.NotNil(t, latest)
	assert.Equal(t, domain.AIAnalysisRunFailed, latest.Status)
	require.NotNil(t, latest.Error)
	assert.Equal(t, "llm provider timed out", *latest.Error)
}

// TestAIAnalysisRunRepository_LatestRun guards the query AlertService/
// IncidentService's Get use to surface the latest AI analysis automatically
// (see domain.Alert/Incident's LatestAnalysis/LatestAnalysisStatus) --
// unlike a "completed only" query, a still-running or failed run must be
// visible too, and the most recent run of any status wins.
func TestAIAnalysisRunRepository_LatestRun(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	alertRepo := repository.NewAlertRepository()
	runsRepo := repository.NewAIAnalysisRunRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	a := newTestAlert(tenantID, domain.SeverityHigh, domain.AlertStatusOpen, nil)
	require.NoError(t, alertRepo.Insert(t.Context(), tx, a))

	t.Run("no runs yet -- nil, not an error", func(t *testing.T) {
		latest, err := runsRepo.LatestRun(t.Context(), tx, "alert", a.ID)
		require.NoError(t, err)
		assert.Nil(t, latest)
	})

	// A system-triggered run (nil ActorID -- see AlertService.EnableAutoAnalysis).
	running := &domain.AIAnalysisRun{
		TenantID: tenantID, ContextType: "alert", ContextID: a.ID, ActorID: nil,
		Status: domain.AIAnalysisRunRunning, Messages: json.RawMessage(`[]`),
		Tools: json.RawMessage(`[]`), ToolRoutes: json.RawMessage(`{}`),
	}
	require.NoError(t, runsRepo.Insert(t.Context(), tx, running))

	t.Run("a still-running run is surfaced as running, not nil", func(t *testing.T) {
		latest, err := runsRepo.LatestRun(t.Context(), tx, "alert", a.ID)
		require.NoError(t, err)
		require.NotNil(t, latest)
		assert.Equal(t, domain.AIAnalysisRunRunning, latest.Status)
		assert.Nil(t, latest.Result)
	})

	require.NoError(t, runsRepo.SetCompleted(t.Context(), tx, running.ID, json.RawMessage(`[]`), "first analysis"))

	t.Run("a completed run's result is returned", func(t *testing.T) {
		latest, err := runsRepo.LatestRun(t.Context(), tx, "alert", a.ID)
		require.NoError(t, err)
		require.NotNil(t, latest)
		assert.Equal(t, domain.AIAnalysisRunCompleted, latest.Status)
		require.NotNil(t, latest.Result)
		assert.Equal(t, "first analysis", *latest.Result)
	})

	second := &domain.AIAnalysisRun{
		TenantID: tenantID, ContextType: "alert", ContextID: a.ID, ActorID: nil,
		Status: domain.AIAnalysisRunRunning, Messages: json.RawMessage(`[]`),
		Tools: json.RawMessage(`[]`), ToolRoutes: json.RawMessage(`{}`),
	}
	require.NoError(t, runsRepo.Insert(t.Context(), tx, second))
	require.NoError(t, runsRepo.SetCompleted(t.Context(), tx, second.ID, json.RawMessage(`[]`), "second analysis"))
	// Every insert in this test shares the same transaction, so created_at
	// (Postgres' transaction-time now()) would otherwise tie between the two
	// runs -- set it explicitly so "most recent wins" is actually deterministic.
	_, err := tx.Exec(t.Context(), `update ai_analysis_runs set created_at = $1 where id = $2`, time.Now().Add(time.Hour), second.ID)
	require.NoError(t, err)

	t.Run("the most recent run wins regardless of status", func(t *testing.T) {
		latest, err := runsRepo.LatestRun(t.Context(), tx, "alert", a.ID)
		require.NoError(t, err)
		require.NotNil(t, latest)
		require.NotNil(t, latest.Result)
		assert.Equal(t, "second analysis", *latest.Result)
	})
}
