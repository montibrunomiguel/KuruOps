create type user_role_enum as enum ('admin', 'analyst', 'viewer');

-- The original admin/analyst/viewer tier label isn't recoverable (roles no
-- longer store it, only is_admin) -- every non-admin role rolls back to
-- 'analyst', the column's original default. resource_access/allowed_tags
-- roll back exactly, since roles still carries both untouched.
alter table users add column role user_role_enum;
update users u set role = (case when r.is_admin then 'admin' else 'analyst' end)::user_role_enum
from roles r where r.id = u.role_id;
alter table users alter column role set not null;
alter table users alter column role set default 'analyst';

alter table users add column resource_access text[];
update users u set resource_access = r.resource_access from roles r where r.id = u.role_id;
alter table users alter column resource_access set not null;
alter table users alter column resource_access set default array['alerts','incidents'];
alter table users add constraint users_resource_access_valid
  check (resource_access <@ array['alerts','incidents','followup']);

alter table users add column allowed_tags text[];
update users u set allowed_tags = r.allowed_tags from roles r where r.id = u.role_id;
alter table users alter column allowed_tags set not null;
alter table users alter column allowed_tags set default '{}';

alter table users drop column role_id;

alter table auth_group_mappings add column role user_role_enum;
update auth_group_mappings m set role = (case when r.is_admin then 'admin' else 'analyst' end)::user_role_enum
from roles r where r.id = m.role_id;
alter table auth_group_mappings alter column role set not null;
alter table auth_group_mappings alter column role set default 'analyst';

alter table auth_group_mappings add column resource_access text[];
update auth_group_mappings m set resource_access = r.resource_access from roles r where r.id = m.role_id;
alter table auth_group_mappings alter column resource_access set not null;
alter table auth_group_mappings alter column resource_access set default array['alerts','incidents'];
alter table auth_group_mappings add constraint auth_group_mappings_resource_access_valid
  check (resource_access <@ array['alerts','incidents','followup']);

alter table auth_group_mappings add column allowed_tags text[];
update auth_group_mappings m set allowed_tags = r.allowed_tags from roles r where r.id = m.role_id;
alter table auth_group_mappings alter column allowed_tags set not null;
alter table auth_group_mappings alter column allowed_tags set default '{}';

alter table auth_group_mappings drop column role_id;

drop table roles;
