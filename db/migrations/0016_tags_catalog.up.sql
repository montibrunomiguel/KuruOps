-- Tag catalog: alerts.tags / incidents.tags stay plain text[] columns (no
-- FK -- see below), but the API layer only lets an analyst attach a tag
-- that exists here, and cmd/ingest drops any webhook-supplied tag that
-- isn't registered instead of silently growing an uncontrolled tag set.
-- This is also the same tag namespace domain.User.AllowedTags scopes
-- access by (see design handoff, "Tag-based + resource-based access
-- scoping") -- a Settings-managed catalog is what makes "company" tags
-- something an admin actually governs instead of whatever string an
-- analyst or a SIEM payload happened to type.
--
-- Deliberately not a foreign key from alerts.tags/incidents.tags: those
-- stay plain text[] (unconstrained at the DB level, same as today) so
-- deleting a tag from the catalog never cascades into rewriting historical
-- alerts/incidents -- a closed alert keeps whatever tags it was closed
-- with, even if that tag is later retired.
create table tags (
  id          uuid primary key default gen_random_uuid(),
  tenant_id   uuid not null references tenants(id) on delete cascade,
  name        text not null,
  color       text,
  created_by  uuid references users(id),
  created_at  timestamptz not null default now()
);

create unique index tags_tenant_name_uq on tags (tenant_id, lower(name));

alter table tags enable row level security;
alter table tags force row level security;

create policy tenant_isolation on tags
  using (tenant_id = current_tenant_id())
  with check (tenant_id = current_tenant_id());
