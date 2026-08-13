-- Associates a webhook endpoint with a field mapping template (see
-- 0042_field_mapping_templates.up.sql). Nullable + on delete set null: an
-- endpoint works exactly as it does today when unset, and deleting a
-- template only clears the association on whatever endpoints used it
-- rather than blocking the delete or cascading into the endpoints
-- themselves. Unlike name/source (fixed at creation, see
-- internal/httpserver/handlers/webhooks.go), this association is meant to
-- change over time as an admin tunes which fields matter -- see
-- WebhookHandlers' new PUT /{id}/field-mapping-template route.
alter table webhook_endpoints
  add column field_mapping_template_id uuid references field_mapping_templates(id) on delete set null;

create index webhook_endpoints_field_mapping_template_id_idx on webhook_endpoints (field_mapping_template_id);
