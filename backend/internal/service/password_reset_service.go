package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/authn"
	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/mailer"
	"github.com/argusops/argusops/internal/repository"
)

// passwordResetTokenTTL is deliberately much shorter than refreshTokenTTL --
// a reset link is a one-time bearer credential mailed in plaintext, so it
// should go stale fast if unused.
const passwordResetTokenTTL = 1 * time.Hour

const passwordResetTokenPrefix = "pr_"

// PasswordResetService is the self-service "forgot my password" flow --
// depends on SMTPConfigService for real delivery (RequestReset degrades to
// a logged no-op if SMTP isn't configured, per its own doc comment below).
type PasswordResetService struct {
	pool       *db.Pool
	repo       *repository.PasswordResetRepository
	users      *repository.UserRepository
	smtp       *SMTPConfigService
	appBaseURL string
}

func NewPasswordResetService(pool *db.Pool, repo *repository.PasswordResetRepository, users *repository.UserRepository, smtp *SMTPConfigService, appBaseURL string) *PasswordResetService {
	return &PasswordResetService{pool: pool, repo: repo, users: users, smtp: smtp, appBaseURL: appBaseURL}
}

// RequestReset always succeeds from the caller's perspective -- it never
// distinguishes "unknown email" from "not a local account" from "SMTP not
// configured" from "email actually sent", logging the real reason
// server-side only. Matches AuthService.LoginLocal's existing discipline of
// not letting a response shape leak account existence.
func (s *PasswordResetService) RequestReset(ctx context.Context, tenantID uuid.UUID, email string) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		u, err := s.users.GetByEmail(ctx, tx, tenantID, email)
		if err != nil {
			return fmt.Errorf("load user: %w", err)
		}
		if u == nil || u.AuthProvider != domain.AuthProviderLocal || !u.IsActive {
			slog.Default().Info("password reset requested for an ineligible account", "email", email)
			return nil
		}

		plaintext, err := generatePrefixedToken(passwordResetTokenPrefix, 32)
		if err != nil {
			return fmt.Errorf("generate reset token: %w", err)
		}
		t := &domain.PasswordResetToken{
			TenantID:  tenantID,
			UserID:    u.ID,
			TokenHash: hashToken(plaintext),
			ExpiresAt: time.Now().Add(passwordResetTokenTTL),
		}
		if err := s.repo.Insert(ctx, tx, t); err != nil {
			return fmt.Errorf("insert reset token: %w", err)
		}

		link := fmt.Sprintf("%s/reset-password?token=%s", s.appBaseURL, plaintext)
		msg := mailer.Message{
			To:      u.Email,
			Subject: "Reset your ArgusOps password",
			Body: fmt.Sprintf(
				"Use the link below to reset your ArgusOps password. It expires in 1 hour.\n\n%s\n\nIf you didn't request this, you can ignore this email.",
				link,
			),
		}
		if err := s.smtp.Send(ctx, tenantID, msg); err != nil {
			slog.Default().Error("password reset email failed to send", "error", err)
		}
		return nil
	})
}

// ConfirmReset validates an unexpired, unused token, rotates the password,
// marks the token used, and invalidates every other outstanding token for
// the same user -- an older still-unexpired link can't also be used
// afterward.
func (s *PasswordResetService) ConfirmReset(ctx context.Context, tenantID uuid.UUID, token, newPassword string) error {
	if err := validatePasswordPolicy(newPassword); err != nil {
		return err
	}
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		t, err := s.repo.GetByHash(ctx, tx, hashToken(token))
		if err != nil {
			return fmt.Errorf("load reset token: %w", err)
		}
		if t == nil || t.UsedAt != nil || t.ExpiresAt.Before(time.Now()) {
			return fmt.Errorf("invalid or expired reset link")
		}

		newHash, err := authn.HashPassword(newPassword)
		if err != nil {
			return fmt.Errorf("hash new password: %w", err)
		}
		if err := s.users.SetPassword(ctx, tx, t.UserID, newHash); err != nil {
			return fmt.Errorf("set password: %w", err)
		}
		if err := s.repo.MarkUsed(ctx, tx, t.ID); err != nil {
			return fmt.Errorf("mark token used: %w", err)
		}
		if err := s.repo.DeleteAllForUser(ctx, tx, t.UserID); err != nil {
			return fmt.Errorf("invalidate other reset tokens: %w", err)
		}
		return nil
	})
}
