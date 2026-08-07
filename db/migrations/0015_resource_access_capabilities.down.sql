-- Lossy: a capability set containing 'followup' (alone or combined) has no
-- exact equivalent in the 3-value enum, so it collapses to the closest of
-- 'both'/'alerts'/'incidents'/'both' (fallback) below.
create type resource_access_enum as enum ('both', 'alerts', 'incidents');

alter table users drop constraint users_resource_access_valid;
alter table users alter column resource_access drop default;
alter table users alter column resource_access type resource_access_enum using (
  case
    when 'alerts' = any(resource_access) and 'incidents' = any(resource_access) then 'both'::resource_access_enum
    when 'alerts' = any(resource_access) then 'alerts'::resource_access_enum
    when 'incidents' = any(resource_access) then 'incidents'::resource_access_enum
    else 'both'::resource_access_enum
  end
);
alter table users alter column resource_access set default 'both';
alter table users alter column resource_access set not null;

alter table auth_group_mappings drop constraint auth_group_mappings_resource_access_valid;
alter table auth_group_mappings alter column resource_access drop default;
alter table auth_group_mappings alter column resource_access type resource_access_enum using (
  case
    when 'alerts' = any(resource_access) and 'incidents' = any(resource_access) then 'both'::resource_access_enum
    when 'alerts' = any(resource_access) then 'alerts'::resource_access_enum
    when 'incidents' = any(resource_access) then 'incidents'::resource_access_enum
    else 'both'::resource_access_enum
  end
);
alter table auth_group_mappings alter column resource_access set default 'both';
alter table auth_group_mappings alter column resource_access set not null;
