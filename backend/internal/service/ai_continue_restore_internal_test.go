package service

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/llmclient"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/testutil"
)

// TestRestoreCompletedRun covers the recovery path for the most ordinary
// failure this feature has: the analyst reads a finished triage report,
// asks a follow-up question, and the LLM answers 503.
//
// Asking a question re-uses the completed run (see continueRun), so that
// 503 used to leave the run 'failed'. The next attempt then saw a failed
// latest run and started a brand new conversation from scratch -- two
// clicks after a finished report, the screen showed nothing but the
// analyst's own unanswered question, and the report was reachable only by
// reading the database.
//
// Written as an internal test because restoreCompletedRun is unexported and
// the alternative -- driving it from the outside -- would need a live LLM
// that can be made to fail on demand.
func TestRestoreCompletedRun(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	svc := &AIAnalysisService{pool: pool, runs: repository.NewAIAnalysisRunRepository()}

	seedCompleted := func(t *testing.T, tenantID uuid.UUID, report string) *domain.AIAnalysisRun {
		t.Helper()
		run := &domain.AIAnalysisRun{
			TenantID: tenantID, ContextType: "alert", ContextID: uuid.New(),
			Status:   domain.AIAnalysisRunRunning,
			Messages: json.RawMessage(`[{"role":"assistant","content":"` + report + `"}]`),
			Tools:    json.RawMessage(`[]`), ToolRoutes: json.RawMessage(`{}`),
		}
		require.NoError(t, pool.WithTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
			if err := svc.runs.Insert(t.Context(), tx, run); err != nil {
				return err
			}
			return svc.runs.SetCompleted(t.Context(), tx, run.ID, run.Messages, report)
		}))
		return run
	}

	reload := func(t *testing.T, tenantID uuid.UUID, id int64) *domain.AIAnalysisRun {
		t.Helper()
		var got *domain.AIAnalysisRun
		require.NoError(t, pool.WithTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
			v, err := svc.runs.Get(t.Context(), tx, id)
			got = v
			return err
		}))
		require.NotNil(t, got)
		return got
	}

	t.Run("a failed follow-up leaves the report completed and readable", func(t *testing.T) {
		tenantID := testutil.NewTenant(t)
		run := seedCompleted(t, tenantID, "Alert Triage Report: TRUE POSITIVE")
		prior := reload(t, tenantID, run.ID).Messages

		// What continueRun does to it: the question is appended and the run
		// goes back to running.
		withQuestion := json.RawMessage(`[{"role":"assistant","content":"Alert Triage Report: TRUE POSITIVE"},{"role":"user","content":"Which Event ID confirms this?"}]`)
		require.NoError(t, pool.WithTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
			return svc.runs.AppendUserMessage(t.Context(), tx, run.ID, withQuestion)
		}))

		snapshot := &completedRunSnapshot{runID: run.ID, messages: prior, result: ptr("Alert Triage Report: TRUE POSITIVE")}
		svc.restoreCompletedRun(t.Context(), tenantID, snapshot, errors.New("llm provider returned 503"))

		got := reload(t, tenantID, run.ID)
		assert.Equal(t, domain.AIAnalysisRunCompleted, got.Status,
			"the analysis is finished; only the follow-up failed")
		assert.JSONEq(t, string(prior), string(got.Messages),
			"the unanswered question is dropped -- leaving it would show a turn the model never replied to")
		require.NotNil(t, got.Result)
		assert.Contains(t, *got.Result, "TRUE POSITIVE")
		require.NotNil(t, got.Error,
			"the cause is what the chat shows so the analyst knows why their question disappeared")
		assert.Equal(t, "llm provider returned 503", *got.Error)
	})

	t.Run("a provider failure is stored with its code and reaches the transcript", func(t *testing.T) {
		// The text is English; the code is what lets the UI show it in the
		// analyst's language. A non-provider error carries no code.
		tenantID := testutil.NewTenant(t)
		run := seedCompleted(t, tenantID, "Alert Triage Report")
		snapshot := &completedRunSnapshot{runID: run.ID, messages: reload(t, tenantID, run.ID).Messages, result: ptr("Alert Triage Report")}
		svc.restoreCompletedRun(t.Context(), tenantID, snapshot, &llmclient.ProviderError{Status: 503, Message: "the provider is busy"})

		got := reload(t, tenantID, run.ID)
		require.NotNil(t, got.ErrorCode)
		assert.Equal(t, "provider_unavailable", *got.ErrorCode)
		transcript, err := buildTranscript(got)
		require.NoError(t, err)
		require.NotNil(t, transcript.ErrorCode)
		assert.Equal(t, "provider_unavailable", *transcript.ErrorCode)

		svc.restoreCompletedRun(t.Context(), tenantID, snapshot, errors.New("database exploded"))
		assert.Nil(t, reload(t, tenantID, run.ID).ErrorCode, "an unclassified failure must not inherit the previous code")
	})

	t.Run("the recorded error does not outlive the next successful turn", func(t *testing.T) {
		// A stale error on a healthy report would make the chat claim the
		// latest question failed when it was answered.
		tenantID := testutil.NewTenant(t)
		run := seedCompleted(t, tenantID, "Alert Triage Report")
		snapshot := &completedRunSnapshot{runID: run.ID, messages: reload(t, tenantID, run.ID).Messages, result: ptr("Alert Triage Report")}
		svc.restoreCompletedRun(t.Context(), tenantID, snapshot, errors.New("llm provider returned 503"))
		require.NotNil(t, reload(t, tenantID, run.ID).Error)

		answered := json.RawMessage(`[{"role":"assistant","content":"Alert Triage Report"},{"role":"user","content":"why?"},{"role":"assistant","content":"because"}]`)
		require.NoError(t, pool.WithTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
			return svc.runs.SetCompleted(t.Context(), tx, run.ID, answered, "because")
		}))

		assert.Nil(t, reload(t, tenantID, run.ID).Error)
	})

	t.Run("asking a new question clears the previous failure", func(t *testing.T) {
		tenantID := testutil.NewTenant(t)
		run := seedCompleted(t, tenantID, "Alert Triage Report")
		snapshot := &completedRunSnapshot{runID: run.ID, messages: reload(t, tenantID, run.ID).Messages, result: ptr("Alert Triage Report")}
		svc.restoreCompletedRun(t.Context(), tenantID, snapshot, errors.New("llm provider returned 503"))

		withQuestion := json.RawMessage(`[{"role":"assistant","content":"Alert Triage Report"},{"role":"user","content":"again?"}]`)
		require.NoError(t, pool.WithTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
			return svc.runs.AppendUserMessage(t.Context(), tx, run.ID, withQuestion)
		}))

		got := reload(t, tenantID, run.ID)
		assert.Equal(t, domain.AIAnalysisRunRunning, got.Status)
		assert.Nil(t, got.Error)
	})

	t.Run("a fresh run that fails is left failed", func(t *testing.T) {
		// There is no earlier good state to return to, so a nil snapshot
		// must be a no-op rather than resurrecting something.
		tenantID := testutil.NewTenant(t)
		run := &domain.AIAnalysisRun{
			TenantID: tenantID, ContextType: "alert", ContextID: uuid.New(),
			Status:   domain.AIAnalysisRunRunning,
			Messages: json.RawMessage(`[]`), Tools: json.RawMessage(`[]`), ToolRoutes: json.RawMessage(`{}`),
		}
		require.NoError(t, pool.WithTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
			if err := svc.runs.Insert(t.Context(), tx, run); err != nil {
				return err
			}
			return svc.runs.SetFailed(t.Context(), tx, run.ID, "llm provider returned 503", "")
		}))

		svc.restoreCompletedRun(t.Context(), tenantID, nil, errors.New("llm provider returned 503"))

		assert.Equal(t, domain.AIAnalysisRunFailed, reload(t, tenantID, run.ID).Status)
	})
}

func ptr(s string) *string { return &s }
