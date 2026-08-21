-- tenants.id cascades to roles and users (see 0001_initial_schema's
-- roles_tenant_id_fkey / users_tenant_id_fkey), so deleting the seeded
-- tenant alone is enough to remove the role and admin user it seeded too.
delete from tenants where slug = 'default';
