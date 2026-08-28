package service_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/db"
	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/testutil"
)

func TestPlaybookService_CRUD(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	svc := service.NewPlaybookService(pool, repository.NewPlaybookRepository(), repository.NewAlertRepository(), "https://kuruops.example")

	pb, err := svc.Create(t.Context(), tenantID, actorID, domain.SavePlaybookInput{
		Title: "Phishing Response", Category: "Phishing",
		Steps: map[domain.IncidentPhase][]domain.SavePlaybookStepInput{domain.PhaseContainment: {{Text: "Disable account"}}},
	})
	require.NoError(t, err)
	assert.NotNil(t, pb.CreatedBy)

	t.Run("get", func(t *testing.T) {
		got, err := svc.Get(t.Context(), tenantID, pb.ID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "Phishing Response", got.Title)
	})

	t.Run("nil keywords normalize to an empty slice, not NULL", func(t *testing.T) {
		got, err := svc.Get(t.Context(), tenantID, pb.ID)
		require.NoError(t, err)
		assert.Equal(t, []string{}, got.Keywords)
	})

	t.Run("update", func(t *testing.T) {
		updated, err := svc.Update(t.Context(), tenantID, pb.ID, domain.SavePlaybookInput{
			Title: "Phishing Response v2", Category: "Phishing",
			Steps: map[domain.IncidentPhase][]domain.SavePlaybookStepInput{domain.PhaseRecovery: {{Text: "Reset password"}}},
		})
		require.NoError(t, err)
		assert.Equal(t, "Phishing Response v2", updated.Title)
	})

	t.Run("delete", func(t *testing.T) {
		require.NoError(t, svc.Delete(t.Context(), tenantID, pb.ID))
		got, err := svc.Get(t.Context(), tenantID, pb.ID)
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}

func TestPlaybookService_MatchForAlertTitle(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	svc := service.NewPlaybookService(pool, repository.NewPlaybookRepository(), repository.NewAlertRepository(), "https://kuruops.example")

	_, err := svc.Create(t.Context(), tenantID, actorID, domain.SavePlaybookInput{
		Title: "Phishing Response", Category: "Phishing", AlertNamePattern: "%phishing%",
	})
	require.NoError(t, err)
	_, err = svc.Create(t.Context(), tenantID, actorID, domain.SavePlaybookInput{
		Title: "General Response", Category: "General Security Event", IsDefault: true,
	})
	require.NoError(t, err)

	t.Run("matches by pattern, case-insensitive", func(t *testing.T) {
		match, err := svc.MatchForAlertTitle(t.Context(), tenantID, "Possible PHISHING attempt detected")
		require.NoError(t, err)
		require.NotNil(t, match)
		assert.Equal(t, "Phishing Response", match.Title)
	})

	t.Run("falls back to the default playbook when nothing matches", func(t *testing.T) {
		match, err := svc.MatchForAlertTitle(t.Context(), tenantID, "Unrelated alert about disk space")
		require.NoError(t, err)
		require.NotNil(t, match)
		assert.Equal(t, "General Response", match.Title)
	})

	t.Run("returns nil when there's no match and no default", func(t *testing.T) {
		otherTenant := testutil.NewTenant(t)
		match, err := svc.MatchForAlertTitle(t.Context(), otherTenant, "Anything")
		require.NoError(t, err)
		assert.Nil(t, match)
	})
}

func insertTestAlert(t *testing.T, pool *db.Pool, tenantID uuid.UUID, alertRepo *repository.AlertRepository, title string) uuid.UUID {
	t.Helper()
	var alertID uuid.UUID
	require.NoError(t, pool.WithTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
		a := &domain.Alert{
			TenantID: tenantID, Title: title, Source: "wazuh",
			Severity: domain.SeverityHigh, OriginalSeverity: domain.SeverityHigh,
			Status: domain.AlertStatusOpen, Tags: []string{}, Payload: json.RawMessage(`{}`), ReceivedAt: time.Now(),
		}
		if err := alertRepo.Insert(t.Context(), tx, a); err != nil {
			return err
		}
		alertID = a.ID
		return nil
	}))
	return alertID
}

func TestPlaybookService_TriggerStepWebhook(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	playbookRepo := repository.NewPlaybookRepository()
	alertRepo := repository.NewAlertRepository()

	var receivedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	svc := service.NewPlaybookService(pool, playbookRepo, alertRepo, "https://kuruops.example")

	pb, err := svc.Create(t.Context(), tenantID, actorID, domain.SavePlaybookInput{
		Title: "Ransomware Response", Category: "Ransomware",
		Steps: map[domain.IncidentPhase][]domain.SavePlaybookStepInput{
			domain.PhaseContainment: {
				{Text: "Isolate host", WebhookURL: srv.URL, WebhookPayloadTemplate: `{"alert":"{{title}}","severity":"{{severity}}"}`},
				{Text: "Notify legal"},
			},
		},
	})
	require.NoError(t, err)
	// Create/Insert never scan the DB-generated step ids back onto the
	// returned Playbook (replaceSteps is a delete-then-reinsert with
	// nothing to scan into) -- a fresh Get is required to learn them.
	created, err := svc.Get(t.Context(), tenantID, pb.ID)
	require.NoError(t, err)
	withWebhook := created.Steps[domain.PhaseContainment][0].ID
	withoutWebhook := created.Steps[domain.PhaseContainment][1].ID

	alertID := insertTestAlert(t, pool, tenantID, alertRepo, "Suspicious login")

	t.Run("fires the webhook with the rendered payload and records a success event", func(t *testing.T) {
		err := svc.TriggerStepWebhook(t.Context(), tenantID, actorID, withWebhook, alertID)
		require.NoError(t, err)
		assert.JSONEq(t, `{"alert":"Suspicious login","severity":"high"}`, string(receivedBody))

		require.NoError(t, pool.WithTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
			var eventType string
			var data []byte
			err := tx.QueryRow(t.Context(), `select event_type, data from alert_events where alert_id = $1 and event_type = 'playbook_webhook_triggered'`, alertID).Scan(&eventType, &data)
			require.NoError(t, err)
			var parsed map[string]any
			require.NoError(t, json.Unmarshal(data, &parsed))
			assert.Equal(t, true, parsed["success"])
			return nil
		}))
	})

	t.Run("a step with no webhook configured returns an error", func(t *testing.T) {
		err := svc.TriggerStepWebhook(t.Context(), tenantID, actorID, withoutWebhook, alertID)
		assert.Error(t, err)
	})

	t.Run("an unreachable destination records a failure event and returns an error", func(t *testing.T) {
		badPb, err := svc.Create(t.Context(), tenantID, actorID, domain.SavePlaybookInput{
			Title: "Bad Endpoint", Category: "Test",
			Steps: map[domain.IncidentPhase][]domain.SavePlaybookStepInput{
				domain.PhaseContainment: {{Text: "Call broken hook", WebhookURL: "http://127.0.0.1:1"}},
			},
		})
		require.NoError(t, err)
		badPbFetched, err := svc.Get(t.Context(), tenantID, badPb.ID)
		require.NoError(t, err)
		badStep := badPbFetched.Steps[domain.PhaseContainment][0].ID

		err = svc.TriggerStepWebhook(t.Context(), tenantID, actorID, badStep, alertID)
		assert.Error(t, err)

		require.NoError(t, pool.WithTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
			var data []byte
			// order by id, not created_at -- two events recorded within the
			// same test can land on the same created_at (timestamp
			// resolution), and id is the reliable insertion-order tiebreaker.
			err := tx.QueryRow(t.Context(), `select data from alert_events where alert_id = $1 and event_type = 'playbook_webhook_triggered' order by id desc limit 1`, alertID).Scan(&data)
			require.NoError(t, err)
			var parsed map[string]any
			require.NoError(t, json.Unmarshal(data, &parsed))
			assert.Equal(t, false, parsed["success"])
			return nil
		}))
	})
}
