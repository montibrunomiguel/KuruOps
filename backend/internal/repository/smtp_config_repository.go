package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/kuruops/kuruops/internal/domain"
)

// SMTPConfigRepository is the single-row-per-tenant "which SMTP relay (if
// any) sends transactional email" config -- same shape as
// StorageConfigRepository.
type SMTPConfigRepository struct{}

func NewSMTPConfigRepository() *SMTPConfigRepository {
	return &SMTPConfigRepository{}
}

func (r *SMTPConfigRepository) Get(ctx context.Context, tx pgx.Tx) (*domain.SMTPConfig, error) {
	var c domain.SMTPConfig
	err := tx.QueryRow(ctx, `
		select tenant_id, host, port, use_tls, username, password_secret_ref,
		       from_address, from_name, created_at, updated_at
		from tenant_smtp_config limit 1`,
	).Scan(
		&c.TenantID, &c.Host, &c.Port, &c.UseTLS, &c.Username, &c.PasswordSecretRef,
		&c.FromAddress, &c.FromName, &c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get smtp config: %w", err)
	}
	return &c, nil
}

// Upsert replaces the tenant's SMTP config wholesale, matching
// StorageConfigRepository.Upsert's shape -- the config is always saved as
// one complete unit (see SMTPConfigService.Save), never a partial field
// update.
func (r *SMTPConfigRepository) Upsert(ctx context.Context, tx pgx.Tx, c *domain.SMTPConfig) error {
	_, err := tx.Exec(ctx, `
		insert into tenant_smtp_config (
			tenant_id, host, port, use_tls, username, password_secret_ref, from_address, from_name
		) values ($1,$2,$3,$4,$5,$6,$7,$8)
		on conflict (tenant_id) do update set
			host = excluded.host, port = excluded.port, use_tls = excluded.use_tls,
			username = excluded.username, password_secret_ref = excluded.password_secret_ref,
			from_address = excluded.from_address, from_name = excluded.from_name,
			updated_at = now()`,
		c.TenantID, c.Host, c.Port, c.UseTLS, c.Username, c.PasswordSecretRef, c.FromAddress, c.FromName,
	)
	if err != nil {
		return fmt.Errorf("upsert smtp config: %w", err)
	}
	return nil
}

// Delete turns off email sending entirely for the tenant.
func (r *SMTPConfigRepository) Delete(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `delete from tenant_smtp_config`)
	if err != nil {
		return fmt.Errorf("delete smtp config: %w", err)
	}
	return nil
}
