-- One LDAP and/or one SAML configuration per tenant (v1: single directory /
-- single IdP per tenant, matching the design handoff's per-tenant Settings
-- screen; a tenant needing multiple IdPs is a later extension, not this
-- table's job to support yet).
create table tenant_ldap_config (
  tenant_id              uuid primary key references tenants(id) on delete cascade,
  host                   text not null,
  port                   integer not null default 636,
  use_tls                boolean not null default true,
  -- service account used to search for the user's DN before the real bind-as-user check
  bind_dn                text not null,
  bind_password_secret_ref text not null,
  user_base_dn           text not null,
  -- e.g. "(mail=%s)" -- %s is replaced with the login email, never interpolated
  -- as literal SQL/LDAP-injectable input (see internal/authn/ldap.go escaping)
  user_filter            text not null default '(mail=%s)',
  group_base_dn          text,
  -- attribute read off the bound user entry to get group membership,
  -- matched against auth_group_mappings.external_group for JIT provisioning
  group_attribute        text not null default 'memberOf',
  created_at             timestamptz not null default now(),
  updated_at             timestamptz not null default now()
);

create table tenant_saml_config (
  tenant_id           uuid primary key references tenants(id) on delete cascade,
  idp_metadata_url    text,
  idp_metadata_xml    text,
  sp_entity_id        text not null,
  acs_url             text not null,
  sp_cert_secret_ref  text not null,
  sp_key_secret_ref   text not null,
  -- SAML attribute (or NameID if empty) read for JIT group provisioning,
  -- matched the same way as the LDAP group_attribute above
  group_attribute     text,
  created_at          timestamptz not null default now(),
  updated_at          timestamptz not null default now(),
  constraint tenant_saml_config_metadata_source
    check (idp_metadata_url is not null or idp_metadata_xml is not null)
);

alter table tenant_ldap_config enable row level security;
alter table tenant_ldap_config force row level security;
create policy tenant_isolation on tenant_ldap_config
  using (tenant_id = current_tenant_id()) with check (tenant_id = current_tenant_id());

alter table tenant_saml_config enable row level security;
alter table tenant_saml_config force row level security;
create policy tenant_isolation on tenant_saml_config
  using (tenant_id = current_tenant_id()) with check (tenant_id = current_tenant_id());

-- Login needs to resolve which tenant's LDAP/SAML config to use before
-- app.tenant_id is set (the user is authenticating precisely to obtain that
-- context) -- same bootstrap problem as the webhook token lookup in
-- 0010_webhook_token_lookup_policy.up.sql, solved the same way: the tenant
-- slug is public and resolved from `tenants` (which was never RLS-scoped),
-- then config lookup happens normally inside WithTenant once the tenant id
-- is known. No policy carve-out is needed here as a result.
