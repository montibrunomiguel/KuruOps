-- Team Notes needs to show who wrote a comment ("Marina Alves", not a raw
-- user id) even to an analyst who has no way to look up another user's name
-- (Settings -> Users & Roles is admin-only). Denormalizing the name at
-- comment-write time avoids adding a new user-lookup endpoint just for this,
-- at the usual denormalization cost: a later name change (Settings, or a
-- re-provisioned federated login) doesn't retroactively update old comments,
-- same tradeoff already accepted for e.g. alerts.original_severity.
alter table incident_comments add column author_name text;

update incident_comments c
set author_name = u.name
from users u
where u.id = c.author_id and c.author_name is null;

alter table incident_comments alter column author_name set not null;
