-- resource_access moves from a mutually-exclusive enum ('both'/'alerts'/
-- 'incidents') to a capability set (text[] containing any combination of
-- 'alerts', 'incidents', 'followup'). Product need: a SOC analyst should be
-- able to see the Follow-up view (SLA-breached incidents + escalated/
-- investigating alerts needing attention) WITHOUT full access to the
-- Incidents section, while a CSIRT member needs any combination up to all
-- three -- a 3-value exclusive enum can't express "follow-up only" as
-- something independent of "incidents". A CHECK constraint replaces the
-- enum's guarantee that only known values are stored; a plain text[] (not a
-- new enum) is deliberate so a future capability doesn't need another
-- multi-step enum migration (see the ADD VALUE + transaction restrictions
-- that made the earlier resource_access_enum a bad fit for anything that
-- changes shape after go-live).
alter table users alter column resource_access drop default;
alter table users alter column resource_access type text[] using (
  case
    -- the seeded default admin (see 0013_seed_default_admin.up.sql) is the
    -- bootstrap superuser and needs to see everything to configure other
    -- users' access; any other role's 'both' collapses to the two original
    -- resources only, same as before -- 'followup' is a new, separately
    -- grantable capability, not something 'both' implied.
    when resource_access = 'both' and role = 'admin' then array['alerts','incidents','followup']
    when resource_access = 'both' then array['alerts','incidents']
    when resource_access = 'alerts' then array['alerts']
    when resource_access = 'incidents' then array['incidents']
  end
);
alter table users alter column resource_access set default array['alerts','incidents'];
alter table users alter column resource_access set not null;
alter table users add constraint users_resource_access_valid
  check (resource_access <@ array['alerts','incidents','followup']);

alter table auth_group_mappings alter column resource_access drop default;
alter table auth_group_mappings alter column resource_access type text[] using (
  case resource_access
    when 'both' then array['alerts','incidents']
    when 'alerts' then array['alerts']
    when 'incidents' then array['incidents']
  end
);
alter table auth_group_mappings alter column resource_access set default array['alerts','incidents'];
alter table auth_group_mappings alter column resource_access set not null;
alter table auth_group_mappings add constraint auth_group_mappings_resource_access_valid
  check (resource_access <@ array['alerts','incidents','followup']);

drop type resource_access_enum;
