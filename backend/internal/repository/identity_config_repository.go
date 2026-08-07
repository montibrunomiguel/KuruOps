package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/domain"
)

type IdentityConfigRepository struct{}

func NewIdentityConfigRepository() *IdentityConfigRepository {
	return &IdentityConfigRepository{}
}

func (r *IdentityConfigRepository) GetLDAPConfig(ctx context.Context, tx pgx.Tx) (*domain.LDAPConfig, error) {
	var c domain.LDAPConfig
	err := tx.QueryRow(ctx, `
		select tenant_id, host, port, use_tls, bind_dn, bind_password_secret_ref,
		       user_base_dn, user_filter, group_base_dn, group_attribute, created_at, updated_at
		from tenant_ldap_config limit 1`,
	).Scan(&c.TenantID, &c.Host, &c.Port, &c.UseTLS, &c.BindDN, &c.BindPasswordSecretRef,
		&c.UserBaseDN, &c.UserFilter, &c.GroupBaseDN, &c.GroupAttribute, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get ldap config: %w", err)
	}
	return &c, nil
}

func (r *IdentityConfigRepository) UpsertLDAPConfig(ctx context.Context, tx pgx.Tx, c *domain.LDAPConfig) error {
	_, err := tx.Exec(ctx, `
		insert into tenant_ldap_config (
			tenant_id, host, port, use_tls, bind_dn, bind_password_secret_ref,
			user_base_dn, user_filter, group_base_dn, group_attribute
		) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		on conflict (tenant_id) do update set
			host = excluded.host, port = excluded.port, use_tls = excluded.use_tls,
			bind_dn = excluded.bind_dn, bind_password_secret_ref = excluded.bind_password_secret_ref,
			user_base_dn = excluded.user_base_dn, user_filter = excluded.user_filter,
			group_base_dn = excluded.group_base_dn, group_attribute = excluded.group_attribute,
			updated_at = now()`,
		c.TenantID, c.Host, c.Port, c.UseTLS, c.BindDN, c.BindPasswordSecretRef,
		c.UserBaseDN, c.UserFilter, c.GroupBaseDN, c.GroupAttribute,
	)
	if err != nil {
		return fmt.Errorf("upsert ldap config: %w", err)
	}
	return nil
}

func (r *IdentityConfigRepository) GetSAMLConfig(ctx context.Context, tx pgx.Tx) (*domain.SAMLConfig, error) {
	var c domain.SAMLConfig
	err := tx.QueryRow(ctx, `
		select tenant_id, idp_metadata_url, idp_metadata_xml, sp_entity_id, acs_url,
		       sp_cert_secret_ref, sp_key_secret_ref, group_attribute, created_at, updated_at
		from tenant_saml_config limit 1`,
	).Scan(&c.TenantID, &c.IDPMetadataURL, &c.IDPMetadataXML, &c.SPEntityID, &c.ACSURL,
		&c.SPCertSecretRef, &c.SPKeySecretRef, &c.GroupAttribute, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get saml config: %w", err)
	}
	return &c, nil
}

func (r *IdentityConfigRepository) UpsertSAMLConfig(ctx context.Context, tx pgx.Tx, c *domain.SAMLConfig) error {
	_, err := tx.Exec(ctx, `
		insert into tenant_saml_config (
			tenant_id, idp_metadata_url, idp_metadata_xml, sp_entity_id, acs_url,
			sp_cert_secret_ref, sp_key_secret_ref, group_attribute
		) values ($1,$2,$3,$4,$5,$6,$7,$8)
		on conflict (tenant_id) do update set
			idp_metadata_url = excluded.idp_metadata_url, idp_metadata_xml = excluded.idp_metadata_xml,
			sp_entity_id = excluded.sp_entity_id, acs_url = excluded.acs_url,
			sp_cert_secret_ref = excluded.sp_cert_secret_ref, sp_key_secret_ref = excluded.sp_key_secret_ref,
			group_attribute = excluded.group_attribute, updated_at = now()`,
		c.TenantID, c.IDPMetadataURL, c.IDPMetadataXML, c.SPEntityID, c.ACSURL,
		c.SPCertSecretRef, c.SPKeySecretRef, c.GroupAttribute,
	)
	if err != nil {
		return fmt.Errorf("upsert saml config: %w", err)
	}
	return nil
}
