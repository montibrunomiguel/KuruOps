package service_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/mailer"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

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
	sender := &fakeSender{}
	svc := service.NewSMTPConfigService(pool, repository.NewSMTPConfigRepository(), secrets.NewEnvStore(), sender)

	t.Run("save without a relay host is rejected", func(t *testing.T) {
		err := svc.Save(t.Context(), tenantID, service.SaveSMTPInput{FromAddress: "a@example.com"})
		assert.ErrorContains(t, err, "required")
	})

	require.NoError(t, svc.Save(t.Context(), tenantID, service.SaveSMTPInput{
		Host: "smtp.example.com", Port: 587, UseTLS: true,
		Username: "smtp-user", Password: "s3cret", FromAddress: "no-reply@example.com", FromName: "ArgusOps",
	}))

	cfg, err := svc.Get(t.Context(), tenantID)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.NotContains(t, cfg.PasswordSecretRef, "s3cret", "the plaintext password never lands in the stored ref")

	t.Run("re-saving without a new password keeps the existing one", func(t *testing.T) {
		require.NoError(t, svc.Save(t.Context(), tenantID, service.SaveSMTPInput{
			Host: "smtp.example.com", Port: 587, UseTLS: true,
			Username: "smtp-user", FromAddress: "alerts@example.com", FromName: "ArgusOps",
		}))
		got, err := svc.Get(t.Context(), tenantID)
		require.NoError(t, err)
		assert.Equal(t, "alerts@example.com", got.FromAddress)
		assert.Equal(t, cfg.PasswordSecretRef, got.PasswordSecretRef)
	})

	t.Run("delete turns email sending off", func(t *testing.T) {
		require.NoError(t, svc.Delete(t.Context(), tenantID))
		got, err := svc.Get(t.Context(), tenantID)
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}

func TestSMTPConfigService_SendTestEmail(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	sender := &fakeSender{}
	svc := service.NewSMTPConfigService(pool, repository.NewSMTPConfigRepository(), secrets.NewEnvStore(), sender)

	t.Run("not configured yet", func(t *testing.T) {
		err := svc.SendTestEmail(t.Context(), tenantID, "someone@example.com")
		assert.ErrorContains(t, err, "not configured")
	})

	require.NoError(t, svc.Save(t.Context(), tenantID, service.SaveSMTPInput{
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
	sender := &fakeSender{}
	svc := service.NewSMTPConfigService(pool, repository.NewSMTPConfigRepository(), secrets.NewEnvStore(), sender)

	require.NoError(t, svc.Save(t.Context(), tenantID, service.SaveSMTPInput{
		Host: "smtp.example.com", Port: 587, UseTLS: true, FromAddress: "no-reply@example.com",
	}))

	msg := mailer.Message{To: "analyst@example.com", Subject: "Password reset", Body: "link"}
	require.NoError(t, svc.Send(t.Context(), tenantID, msg))
	require.Len(t, sender.sent, 1)
	assert.Equal(t, msg, sender.sent[0])
}
