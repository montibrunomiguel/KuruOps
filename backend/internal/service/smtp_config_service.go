package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/mailer"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
)

// smtpConfigRepo is the subset of *repository.SMTPConfigRepository this
// service calls -- an interface, not the concrete type, purely so tests can
// substitute a repo double that fails on demand to exercise the
// error-wrapping branches (a DB call failing mid-transaction) a real
// Postgres integration test can't trigger. *repository.SMTPConfigRepository
// already satisfies this implicitly, so every existing constructor call
// site is unaffected.
type smtpConfigRepo interface {
	Get(ctx context.Context, tx pgx.Tx) (*domain.SMTPConfig, error)
	Upsert(ctx context.Context, tx pgx.Tx, c *domain.SMTPConfig) error
	Delete(ctx context.Context, tx pgx.Tx) error
}

// SMTPConfigService is Settings -> SMTP: lets an admin point outbound
// transactional email (password reset, and any future notification) at a
// real relay. Mirrors StorageConfigService's shape -- same "empty string on
// save means keep the existing secret" convention.
type SMTPConfigService struct {
	pool    *db.Pool
	repo    smtpConfigRepo
	secrets secrets.Store
	sender  mailer.Sender
	audit   *repository.AdminAuditEventRepository
}

func NewSMTPConfigService(pool *db.Pool, repo smtpConfigRepo, store secrets.Store, sender mailer.Sender, audit *repository.AdminAuditEventRepository) *SMTPConfigService {
	return &SMTPConfigService{pool: pool, repo: repo, secrets: store, sender: sender, audit: audit}
}

func smtpConfigAuditFields(c *domain.SMTPConfig) map[string]any {
	if c == nil {
		return nil
	}
	return map[string]any{
		"host": c.Host, "port": c.Port, "useTLS": c.UseTLS, "username": c.Username,
		"passwordSet": c.PasswordSecretRef != "", "fromAddress": c.FromAddress, "fromName": c.FromName,
	}
}

func (s *SMTPConfigService) Get(ctx context.Context, tenantID uuid.UUID) (*domain.SMTPConfig, error) {
	var cfg *domain.SMTPConfig
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		c, err := s.repo.Get(ctx, tx)
		cfg = c
		return err
	})
	return cfg, err
}

func (s *SMTPConfigService) Delete(ctx context.Context, tenantID, actorID uuid.UUID) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		existing, err := s.repo.Get(ctx, tx)
		if err != nil {
			return err
		}
		if err := s.repo.Delete(ctx, tx); err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]any{"from": smtpConfigAuditFields(existing), "to": nil})
		return s.audit.InsertEvent(ctx, tx, &domain.AdminAuditEvent{
			TenantID: tenantID, Area: "smtp-config", Action: "delete", ActorType: domain.ActorUser, ActorID: actorID, Data: data,
		})
	})
}

type SaveSMTPInput struct {
	Host        string
	Port        int
	UseTLS      bool
	Username    string
	Password    string // plaintext; "" on update means keep existing
	FromAddress string
	FromName    string // "" means no display name
}

func (s *SMTPConfigService) Save(ctx context.Context, tenantID, actorID uuid.UUID, in SaveSMTPInput) error {
	if in.Host == "" || in.Port <= 0 || in.FromAddress == "" {
		return fmt.Errorf("host, port, and fromAddress are required")
	}
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		before, err := s.repo.Get(ctx, tx)
		if err != nil {
			return fmt.Errorf("load existing smtp config: %w", err)
		}
		ref, err := s.resolveSecretRef(ctx, tx, tenantID, in.Password)
		if err != nil {
			return err
		}
		var fromName *string
		if in.FromName != "" {
			fromName = &in.FromName
		}
		cfg := &domain.SMTPConfig{
			TenantID: tenantID, Host: in.Host, Port: in.Port, UseTLS: in.UseTLS,
			Username: in.Username, PasswordSecretRef: ref,
			FromAddress: in.FromAddress, FromName: fromName,
		}
		if err := s.repo.Upsert(ctx, tx, cfg); err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]any{"from": smtpConfigAuditFields(before), "to": smtpConfigAuditFields(cfg)})
		return s.audit.InsertEvent(ctx, tx, &domain.AdminAuditEvent{
			TenantID: tenantID, Area: "smtp-config", Action: "save", ActorType: domain.ActorUser, ActorID: actorID, Data: data,
		})
	})
}

// resolveSecretRef is SMTP's use of secrets.PutOrKeepExisting: a new
// password is always stored fresh; a blank one falls back to whatever was
// already saved. Unlike StorageConfigService, an empty result is left as
// "" rather than rejected -- an SMTP relay with no auth at all
// (in.Username == "") still calls this with an empty password and simply
// gets an empty ref back, which is fine since Send only authenticates when
// Username is non-empty.
func (s *SMTPConfigService) resolveSecretRef(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, plaintext string) (string, error) {
	existing, err := s.repo.Get(ctx, tx)
	if err != nil {
		return "", fmt.Errorf("load existing smtp config: %w", err)
	}
	existingRef := ""
	if existing != nil {
		existingRef = existing.PasswordSecretRef
	}
	return secrets.PutOrKeepExisting(ctx, s.secrets, tenantID.String(), "smtp-password", plaintext, existingRef)
}

// resolve builds the mailer.Config for the tenant's current SMTP settings,
// used by both SendTestEmail and Send.
func (s *SMTPConfigService) resolve(ctx context.Context, tx pgx.Tx) (mailer.Config, error) {
	cfg, err := s.repo.Get(ctx, tx)
	if err != nil {
		return mailer.Config{}, fmt.Errorf("load smtp config: %w", err)
	}
	if cfg == nil {
		return mailer.Config{}, fmt.Errorf("smtp is not configured")
	}
	var password string
	if cfg.PasswordSecretRef != "" {
		password, err = s.secrets.Resolve(ctx, cfg.PasswordSecretRef)
		if err != nil {
			return mailer.Config{}, fmt.Errorf("resolve smtp password: %w", err)
		}
	}
	from := cfg.FromAddress
	if cfg.FromName != nil && *cfg.FromName != "" {
		from = fmt.Sprintf("%s <%s>", *cfg.FromName, cfg.FromAddress)
	}
	return mailer.Config{
		Host: cfg.Host, Port: cfg.Port, UseTLS: cfg.UseTLS,
		Username: cfg.Username, Password: password, From: from,
	}, nil
}

// SendTestEmail lets an admin confirm their SMTP settings actually work
// before relying on them for password resets.
func (s *SMTPConfigService) SendTestEmail(ctx context.Context, tenantID uuid.UUID, to string) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		cfg, err := s.resolve(ctx, tx)
		if err != nil {
			return err
		}
		return s.sender.Send(ctx, cfg, mailer.Message{
			To:      to,
			Subject: "ArgusOps test email",
			Body:    "This is a test email from ArgusOps to confirm your SMTP configuration is working.",
		})
	})
}

// Send delivers an application-generated message (e.g. a password reset
// link) through the tenant's configured relay. Callers (PasswordResetService)
// must treat a "smtp is not configured" error as an expected, non-fatal
// case -- see that service's RequestReset for why.
func (s *SMTPConfigService) Send(ctx context.Context, tenantID uuid.UUID, msg mailer.Message) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		cfg, err := s.resolve(ctx, tx)
		if err != nil {
			return err
		}
		return s.sender.Send(ctx, cfg, msg)
	})
}
