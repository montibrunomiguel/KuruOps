-- Rotation/expiration policy for webhook tokens: expires_at is nullable
-- (null = never expires, an explicit admin opt-out) but Create/Regenerate
-- default to a 90-day expiry in the service layer so tokens don't live
-- forever by accident. cmd/ingest rejects a token past its expires_at with
-- the same 401 it already uses for an unknown/invalid token.
alter table webhook_endpoints
  add column expires_at timestamptz;
