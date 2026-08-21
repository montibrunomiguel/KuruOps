-- Default off: today, an alert only gets AI analysis when an analyst
-- clicks "Analyze with AI" (or previously, unconditionally on every webhook
-- ingest -- see AlertService.EnableAutoAnalysis). This flips that default:
-- auto-analysis on ingest now only fires for a provider that opts in here,
-- so a tenant's LLM spend/rate-limit exposure isn't a surprise the moment a
-- provider is configured.
alter table llm_providers add column auto_analyze_all_alerts boolean not null default false;
