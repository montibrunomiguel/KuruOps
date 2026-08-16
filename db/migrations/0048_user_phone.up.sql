-- Users may register a phone number alongside their email -- surfaced in
-- Escala de Acionamento's webhook payload placeholders ({{analystPhone}})
-- so a custom escalation webhook can page the resolved on-call analyst by
-- phone, not just email. Free-text, no format constraint (same as every
-- other admin-entered text field in this schema) -- international numbers
-- vary too much to validate meaningfully here.
alter table users add column phone text;
