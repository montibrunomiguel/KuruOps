alter table incidents add column owner_id uuid references users(id);

-- Best-effort backfill: an incident that ended up with multiple assignees
-- can only keep one owner_id, so this picks whichever was assigned first.
update incidents i
set owner_id = a.user_id
from (
  select distinct on (incident_id) incident_id, user_id
  from incident_assignees
  order by incident_id, created_at asc
) a
where a.incident_id = i.id;

drop table incident_assignees;
