-- One SMTP relay per tenant, used to send the forgot-password link
-- (service.PasswordResetService) and any future transactional email. Same
-- single-row-per-tenant shape as tenant_storage_config
-- (0019_tenant_storage_config.up.sql) -- no row means email sending is not
-- configured for this tenant, not an error; see
-- PasswordResetService.RequestReset for what happens to a reset request in
-- that case (it still succeeds, silently, to avoid leaking account
-- existence via response shape).
create table tenant_smtp_config (
  tenant_id           uuid primary key references tenants(id) on delete cascade,
  host                text not null,
  port                integer not null default 587,
  use_tls             boolean not null default true,
  username            text not null,
  password_secret_ref text not null,
  from_address        text not null,
  from_name           text,
  created_at          timestamptz not null default now(),
  updated_at          timestamptz not null default now()
);

alter table tenant_smtp_config enable row level security;
alter table tenant_smtp_config force row level security;

create policy tenant_isolation on tenant_smtp_config
  using (tenant_id = current_tenant_id())
  with check (tenant_id = current_tenant_id());
