-- Lets a webhook escalation channel send a custom JSON body instead of the
-- fixed shape notifier.WebhookSender has always sent -- nullable, only
-- meaningful when channel_type = 'webhook'; null preserves today's
-- behavior exactly.
alter table escalation_policies add column webhook_payload_template text;
