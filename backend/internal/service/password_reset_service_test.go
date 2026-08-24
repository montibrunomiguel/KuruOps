package service_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/authn"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

// extractResetToken pulls the plaintext token out of the "...?token=XYZ"
// link embedded in the reset email body -- the service intentionally never
// returns the token directly (it's mailed, not handed back to the caller),
// so tests have to read it the same way a real recipient would.
func extractResetToken(t *testing.T, emailBody string) string {
	t.Helper()
	_, after, found := strings.Cut(emailBody, "token=")
	require.True(t, found, "email body must contain a reset link with a token")
	token, _, _ := strings.Cut(after, "\n")
	return token
}

func newPasswordResetService(t *testing.T, sender *fakeSender) (*service.PasswordResetService, *service.UserService) {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	userRepo := repository.NewUserRepository()
	smtpSvc := service.NewSMTPConfigService(pool, repository.NewSMTPConfigRepository(), secrets.NewEnvStore(), sender, repository.NewAdminAuditEventRepository())
	resetSvc := service.NewPasswordResetService(pool, repository.NewPasswordResetRepository(), userRepo, smtpSvc, "http://localhost:3000")
	return resetSvc, service.NewUserService(pool, userRepo, repository.NewAdminAuditEventRepository())
}

func TestPasswordResetService_RequestReset(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	sender := &fakeSender{}
	resetSvc, userSvc := newPasswordResetService(t, sender)

	t.Run("unknown email always succeeds and sends nothing", func(t *testing.T) {
		err := resetSvc.RequestReset(t.Context(), tenantID, "nobody@test.local")
		require.NoError(t, err)
		assert.Empty(t, sender.sent)
	})

	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	user, _, err := userSvc.CreateLocal(t.Context(), tenantID, actorID, "resetme@test.local", "Reset Me", "", testutil.NewRole(t, tenantID, false, []string{"alerts"}))
	require.NoError(t, err)

	t.Run("known user but SMTP not configured still succeeds, sends nothing", func(t *testing.T) {
		err := resetSvc.RequestReset(t.Context(), tenantID, user.Email)
		require.NoError(t, err)
		assert.Empty(t, sender.sent, "no SMTPConfigService.Save has happened yet in this test")
	})

	smtpSvc := service.NewSMTPConfigService(pool, repository.NewSMTPConfigRepository(), secrets.NewEnvStore(), sender, repository.NewAdminAuditEventRepository())
	require.NoError(t, smtpSvc.Save(t.Context(), tenantID, actorID, service.SaveSMTPInput{
		Host: "smtp.example.com", Port: 587, UseTLS: true, FromAddress: "no-reply@example.com", Password: "x",
	}))

	t.Run("known local active user with SMTP configured sends a reset link", func(t *testing.T) {
		require.NoError(t, resetSvc.RequestReset(t.Context(), tenantID, user.Email))
		require.Len(t, sender.sent, 1)
		assert.Equal(t, user.Email, sender.sent[0].To)
		assert.Contains(t, sender.sent[0].Body, "http://localhost:3000/reset-password?token=")
	})
}

func TestPasswordResetService_ConfirmReset(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	sender := &fakeSender{}
	resetSvc, userSvc := newPasswordResetService(t, sender)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	smtpSvc := service.NewSMTPConfigService(pool, repository.NewSMTPConfigRepository(), secrets.NewEnvStore(), sender, repository.NewAdminAuditEventRepository())
	require.NoError(t, smtpSvc.Save(t.Context(), tenantID, actorID, service.SaveSMTPInput{
		Host: "smtp.example.com", Port: 587, UseTLS: true, FromAddress: "no-reply@example.com", Password: "x",
	}))

	user, _, err := userSvc.CreateLocal(t.Context(), tenantID, actorID, "resetme2@test.local", "Reset Me", "", testutil.NewRole(t, tenantID, false, []string{"alerts"}))
	require.NoError(t, err)

	t.Run("rejects an unknown token", func(t *testing.T) {
		err := resetSvc.ConfirmReset(t.Context(), tenantID, "no-such-token", "NewPassword123!")
		assert.ErrorContains(t, err, "invalid or expired")
	})

	t.Run("rejects a new password shorter than 8 characters", func(t *testing.T) {
		err := resetSvc.ConfirmReset(t.Context(), tenantID, "whatever", "short")
		assert.ErrorContains(t, err, "at least 8 characters")
	})

	t.Run("rejects a new password with only digits", func(t *testing.T) {
		err := resetSvc.ConfirmReset(t.Context(), tenantID, "whatever", "12345678")
		assert.ErrorContains(t, err, "at least one letter and one digit")
	})

	require.NoError(t, resetSvc.RequestReset(t.Context(), tenantID, user.Email))
	require.Len(t, sender.sent, 1)
	token := extractResetToken(t, sender.sent[0].Body)

	t.Run("a correct token resets the password", func(t *testing.T) {
		require.NoError(t, resetSvc.ConfirmReset(t.Context(), tenantID, token, "BrandNewPassword123!"))

		got, err := userSvc.Get(t.Context(), tenantID, user.ID)
		require.NoError(t, err)
		ok, err := authn.VerifyPassword(*got.PasswordHash, "BrandNewPassword123!")
		require.NoError(t, err)
		assert.True(t, ok)

		t.Run("the same token cannot be reused", func(t *testing.T) {
			err := resetSvc.ConfirmReset(t.Context(), tenantID, token, "AnotherPassword123!")
			assert.ErrorContains(t, err, "invalid or expired")
		})
	})
}
