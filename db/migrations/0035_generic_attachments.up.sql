-- Uploads stopped being image-only (see UploadHandlers) -- rename every
-- column that held that assumption in its name. Pure renames, no data
-- movement: existing URLs under /api/v1/uploads/images/... keep working
-- unchanged (that route prefix is left as-is on purpose, see uploads.go).
alter table alerts rename column close_image_url to close_attachment_url;
alter table alert_comments rename column image_url to attachment_url;
alter table incident_comments rename column image_url to attachment_url;
