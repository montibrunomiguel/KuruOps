create table tenants (
  id          uuid primary key default gen_random_uuid(),
  name        text not null,
  slug        text not null unique,
  created_at  timestamptz not null default now()
);

create table users (
  id              uuid primary key default gen_random_uuid(),
  tenant_id       uuid not null references tenants(id) on delete cascade,
  email           citext not null,
  name            text not null,
  auth_provider   auth_provider_enum not null default 'local',
  -- LDAP distinguished name or SAML NameID; null for local accounts
  external_id     text,
  password_hash   text,
  role            user_role_enum not null default 'analyst',
  resource_access resource_access_enum not null default 'both',
  -- empty array = unrestricted, sees all tags (matches prototype semantics)
  allowed_tags    text[] not null default '{}',
  mfa_totp_secret text,
  is_active       boolean not null default true,
  last_login_at   timestamptz,
  created_at      timestamptz not null default now(),
  updated_at      timestamptz not null default now(),
  constraint users_local_requires_password
    check (auth_provider <> 'local' or password_hash is not null),
  constraint users_federated_requires_external_id
    check (auth_provider = 'local' or external_id is not null)
);

create unique index users_tenant_email_uq on users (tenant_id, email);
create index users_tenant_id_idx on users (tenant_id);
create index users_external_id_idx on users (tenant_id, auth_provider, external_id);

-- Just-in-time provisioning: maps an LDAP group or SAML assertion attribute
-- value to a role + resource_access + allowed_tags, applied on every login
-- so group membership changes in the IdP take effect without manual edits.
create table auth_group_mappings (
  id              uuid primary key default gen_random_uuid(),
  tenant_id       uuid not null references tenants(id) on delete cascade,
  provider        auth_provider_enum not null,
  external_group  text not null,
  role            user_role_enum not null default 'analyst',
  resource_access resource_access_enum not null default 'both',
  allowed_tags    text[] not null default '{}',
  created_at      timestamptz not null default now(),
  constraint auth_group_mappings_provider_check check (provider in ('ldap', 'saml'))
);

create unique index auth_group_mappings_uq
  on auth_group_mappings (tenant_id, provider, external_group);
