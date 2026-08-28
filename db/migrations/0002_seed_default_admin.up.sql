-- Seeds the one tenant every deployment resolves automatically (see
-- TenantRepository.GetDefault -- KuruOps is single-instance, self-hosted
-- software: there is exactly one row in `tenants`, never chosen at login)
-- plus a default admin, so a fresh deploy is usable immediately without a
-- manual SQL bootstrap step -- see backend/README.md and
-- scripts/smoke-test.sh.
--
-- Credentials: admin@kuruops.local / ChangeMe123!
-- The hash below is fixed and public (this file ships in the open-source
-- repo) -- that is fine ONLY because must_change_password locks the
-- account down to just the change-password endpoint until it's rotated
-- (see middleware.RequirePasswordChanged). Never disable that gate.
--
-- Kept as its own migration, separate from 0001_initial_schema, so
-- internal/dbmigrate's external-database-migration feature can cleanly
-- undo just this seed (see Service.clearSeedData) before copying a real
-- customer's tenant over the top -- that only works because nothing else
-- in the migration set inserts into `tenants`, so the row this migration
-- creates is always the only one present at that point.
do $$
declare
  v_tenant_id uuid;
  v_role_id uuid;
begin
  if not exists (select 1 from tenants) then
    insert into tenants (name, slug) values ('KuruOps', 'default')
    returning id into v_tenant_id;

    insert into roles (tenant_id, name, is_admin, resource_access, allowed_tags)
    values (v_tenant_id, 'Admin', true, array['alerts', 'incidents', 'followup'], '{}')
    returning id into v_role_id;

    insert into users (
      tenant_id, email, name, auth_provider, password_hash,
      role_id, must_change_password
    ) values (
      v_tenant_id, 'admin@kuruops.local', 'Admin', 'local',
      '$argon2id$v=19$m=19456,t=2,p=1$FY5wcI4uEMERBPed8Udzmg$VyPXi8Lxn0/sm9I42qDYzgOsxUTerYSI4RmVkIiGq8Y',
      v_role_id, true
    );
  end if;
end
$$;
