-- ArgusOps is single-instance, self-hosted software (see architecture
-- review + README): one deployment, "companies" exist only as tags on
-- alerts/incidents for access scoping, not as a login-time concept. There
-- is still exactly one row in `tenants` under the hood -- every table keeps
-- its tenant_id / RLS policy so this could grow into real multi-tenant SaaS
-- later without a schema rewrite, but the API and every login flow always
-- resolve "the" tenant automatically (see TenantRepository.GetDefault) and
-- never ask for it.
--
-- This migration seeds that one tenant plus a default admin account so a
-- fresh deploy is usable immediately without a manual SQL bootstrap step —
-- see backend/README.md and scripts/smoke-test.sh.
--
-- Credentials: admin@argusops.local / ChangeMe123!
-- The hash below is fixed and public (this file ships in the open-source
-- repo) -- that is fine ONLY because must_change_password locks the account
-- down to just the change-password endpoint until it's rotated (see
-- middleware.RequirePasswordChanged). Never disable that gate.
do $$
declare
  v_tenant_id uuid;
begin
  if not exists (select 1 from tenants) then
    insert into tenants (name, slug) values ('ArgusOps', 'default')
    returning id into v_tenant_id;

    insert into users (
      tenant_id, email, name, auth_provider, password_hash,
      role, resource_access, must_change_password
    ) values (
      v_tenant_id, 'admin@argusops.local', 'Admin', 'local',
      '$argon2id$v=19$m=19456,t=2,p=1$FY5wcI4uEMERBPed8Udzmg$VyPXi8Lxn0/sm9I42qDYzgOsxUTerYSI4RmVkIiGq8Y',
      'admin', 'both', true
    );
  end if;
end
$$;
