package service_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/mailer"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

// fakeSMTPConfigRepo lets a test fail a specific repo call on demand --
// SMTPConfigService takes an interface (not the concrete
// *repository.SMTPConfigRepository) specifically so this is possible. See
// fakeStorageConfigRepo (storage_config_service_test.go) for the fuller
// version of this reasoning.
type fakeSMTPConfigRepo struct {
	getErr    error
	upsertErr error
	deleteErr error
}

func (f *fakeSMTPConfigRepo) Get(context.Context, pgx.Tx) (*domain.SMTPConfig, error) {
	return nil, f.getErr
}
func (f *fakeSMTPConfigRepo) Upsert(context.Context, pgx.Tx, *domain.SMTPConfig) error {
	return f.upsertErr
}
func (f *fakeSMTPConfigRepo) Delete(context.Context, pgx.Tx) error { return f.deleteErr }

// fakeSender records every message it was asked to send, letting tests
// assert delivery happened without a real network dependency.
type fakeSender struct {
	sent    []mailer.Message
	failNil bool // when true, Send always fails -- simulates a bad relay
}

func (f *fakeSender) Send(_ context.Context, _ mailer.Config, msg mailer.Message) error {
	if f.failNil {
		return fmt.Errorf("simulated relay failure")
	}
	f.sent = append(f.sent, msg)
	return nil
}

func TestSMTPConfigService_Save(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	sender := &fakeSender{}
	auditRepo := repository.NewAdminAuditEventRepository()
	svc := service.NewSMTPConfigService(pool, repository.NewSMTPConfigRepository(), secrets.NewEnvStore(), sender, auditRepo)

	t.Run("save without a relay host is rejected", func(t *testing.T) {
		err := svc.Save(t.Context(), tenantID, actorID, service.SaveSMTPInput{FromAddress: "a@example.com"})
		assert.ErrorContains(t, err, "required")
	})

	require.NoError(t, svc.Save(t.Context(), tenantID, actorID, service.SaveSMTPInput{
		Host: "smtp.example.com", Port: 587, UseTLS: true,
		Username: "smtp-user", Password: "s3cret", FromAddress: "no-reply@example.com", FromName: "ArgusOps",
	}))

	cfg, err := svc.Get(t.Context(), tenantID)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.NotContains(t, cfg.PasswordSecretRef, "s3cret", "the plaintext password never lands in the stored ref")

	t.Run("re-saving without a new password keeps the existing one", func(t *testing.T) {
		require.NoError(t, svc.Save(t.Context(), tenantID, actorID, service.SaveSMTPInput{
			Host: "smtp.example.com", Port: 587, UseTLS: true,
			Username: "smtp-user", FromAddress: "alerts@example.com", FromName: "ArgusOps",
		}))
		got, err := svc.Get(t.Context(), tenantID)
		require.NoError(t, err)
		assert.Equal(t, "alerts@example.com", got.FromAddress)
		assert.Equal(t, cfg.PasswordSecretRef, got.PasswordSecretRef)
	})

	t.Run("delete turns email sending off", func(t *testing.T) {
		require.NoError(t, svc.Delete(t.Context(), tenantID, actorID))
		got, err := svc.Get(t.Context(), tenantID)
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("save and delete each record an admin audit event", func(t *testing.T) {
		tx := testutil.BeginTx(t, pool, tenantID)
		events, err := auditRepo.List(t.Context(), tx, nil, 10)
		require.NoError(t, err)
		var actions []string
		for _, e := range events {
			assert.Equal(t, "smtp-config", e.Area)
			actions = append(actions, e.Action)
		}
		assert.Contains(t, actions, "save")
		assert.Contains(t, actions, "delete")
	})
}

func TestSMTPConfigService_SendTestEmail(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	sender := &fakeSender{}
	svc := service.NewSMTPConfigService(pool, repository.NewSMTPConfigRepository(), secrets.NewEnvStore(), sender, repository.NewAdminAuditEventRepository())

	t.Run("not configured yet", func(t *testing.T) {
		err := svc.SendTestEmail(t.Context(), tenantID, "someone@example.com")
		assert.ErrorContains(t, err, "not configured")
	})

	require.NoError(t, svc.Save(t.Context(), tenantID, actorID, service.SaveSMTPInput{
		Host: "smtp.example.com", Port: 587, UseTLS: true,
		Username: "smtp-user", Password: "s3cret", FromAddress: "no-reply@example.com",
	}))

	require.NoError(t, svc.SendTestEmail(t.Context(), tenantID, "someone@example.com"))
	require.Len(t, sender.sent, 1)
	assert.Equal(t, "someone@example.com", sender.sent[0].To)
}

func TestSMTPConfigService_Send(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	sender := &fakeSender{}
	svc := service.NewSMTPConfigService(pool, repository.NewSMTPConfigRepository(), secrets.NewEnvStore(), sender, repository.NewAdminAuditEventRepository())

	require.NoError(t, svc.Save(t.Context(), tenantID, actorID, service.SaveSMTPInput{
		Host: "smtp.example.com", Port: 587, UseTLS: true, FromAddress: "no-reply@example.com",
	}))

	msg := mailer.Message{To: "analyst@example.com", Subject: "Password reset", Body: "link"}
	require.NoError(t, svc.Send(t.Context(), tenantID, msg))
	require.Len(t, sender.sent, 1)
	assert.Equal(t, msg, sender.sent[0])
}

// TestSMTPConfigService_RepoErrors exercises Save/Delete's "load existing
// config to build the audit diff, then persist" error-wrapping branches --
// unreachable via a real Postgres integration test.
func TestSMTPConfigService_RepoErrors(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	sender := &fakeSender{}
	validInput := service.SaveSMTPInput{Host: "smtp.example.com", Port: 587, FromAddress: "no-reply@example.com"}

	t.Run("Save wraps a Get failure", func(t *testing.T) {
		svc := service.NewSMTPConfigService(pool, &fakeSMTPConfigRepo{getErr: errors.New("get boom")}, secrets.NewEnvStore(), sender, repository.NewAdminAuditEventRepository())
		err := svc.Save(t.Context(), tenantID, actorID, validInput)
		assert.ErrorContains(t, err, "get boom")
	})

	t.Run("Save wraps an Upsert failure", func(t *testing.T) {
		svc := service.NewSMTPConfigService(pool, &fakeSMTPConfigRepo{upsertErr: errors.New("upsert boom")}, secrets.NewEnvStore(), sender, repository.NewAdminAuditEventRepository())
		err := svc.Save(t.Context(), tenantID, actorID, validInput)
		assert.ErrorContains(t, err, "upsert boom")
	})

	t.Run("Delete wraps a Get failure", func(t *testing.T) {
		svc := service.NewSMTPConfigService(pool, &fakeSMTPConfigRepo{getErr: errors.New("get boom")}, secrets.NewEnvStore(), sender, repository.NewAdminAuditEventRepository())
		err := svc.Delete(t.Context(), tenantID, actorID)
		assert.ErrorContains(t, err, "get boom")
	})

	t.Run("Delete wraps a Delete failure", func(t *testing.T) {
		svc := service.NewSMTPConfigService(pool, &fakeSMTPConfigRepo{deleteErr: errors.New("delete boom")}, secrets.NewEnvStore(), sender, repository.NewAdminAuditEventRepository())
		err := svc.Delete(t.Context(), tenantID, actorID)
		assert.ErrorContains(t, err, "delete boom")
	})
}
