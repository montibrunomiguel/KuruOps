-- ai_analysis_jobs: the durable queue behind "Analyze every incoming alert
-- automatically".
--
-- That analysis used to be a bare goroutine started by the ingest process the
-- moment an alert landed: no record that it was owed, so a restart, a deploy or
-- an OOM between "alert stored" and "analysis finished" lost it silently, and a
-- provider 503 ended it for good because nothing ever tried again. One row here
-- per alert makes the obligation durable: it is written in the SAME transaction
-- as the alert (see AlertService.Ingest), so an alert that exists always has its
-- analysis owed, and a consumer works through the rows with retry and backoff.
--
-- One job per alert (the unique index), so a re-delivered webhook or a retried
-- insert cannot queue a second analysis of the same alert.
--
-- Lifecycle, driven by service.AIAnalysisQueue:
--   pending   -> waiting for next_attempt_at
--   running   -> claimed by a consumer; locked_until is its lease, renewed while
--                it works. A consumer that dies stops renewing, the lease runs
--                out, and the next claim picks the job up again.
--   succeeded -> analysed
--   skipped   -> nothing to do and never will be (auto-analysis was turned off,
--                no provider configured, the alert was deleted)
--   failed    -> gave up: a non-retryable error (bad API key / URL / model) or
--                max_attempts reached. last_error says why.
create table ai_analysis_jobs (
  id              bigint generated always as identity primary key,
  tenant_id       uuid not null references tenants(id) on delete cascade,
  -- on delete cascade: deleting an alert (data retention does) takes its job with it.
  alert_id        uuid not null references alerts(id) on delete cascade,
  status          text not null default 'pending'
                    check (status in ('pending', 'running', 'succeeded', 'failed', 'skipped')),
  attempts        integer not null default 0,
  max_attempts    integer not null default 5,
  next_attempt_at timestamptz not null default now(),
  locked_until    timestamptz,
  last_error      text,
  created_at      timestamptz not null default now(),
  updated_at      timestamptz not null default now(),
  constraint ai_analysis_jobs_alert_uq unique (alert_id)
);

-- The two lookups the consumer does on every poll: jobs that are due, and jobs
-- whose lease has expired. Partial indexes, so they stay tiny however many
-- finished jobs the table holds.
create index ai_analysis_jobs_due_idx on ai_analysis_jobs (next_attempt_at) where status = 'pending';
create index ai_analysis_jobs_lease_idx on ai_analysis_jobs (locked_until) where status = 'running';

alter table ai_analysis_jobs enable row level security;
alter table ai_analysis_jobs force row level security;

-- Every ordinary path (enqueue at ingest, an analyst's request) is tenant-scoped
-- like every other table.
create policy tenant_isolation on ai_analysis_jobs
  using (tenant_id = current_tenant_id())
  with check (tenant_id = current_tenant_id());

-- The consumer is the exception: one process works the queue for ALL tenants, and
-- it cannot know a job's tenant before it has read the job. Same narrow, opt-in
-- carve-out shape as webhook_token_lookup / api_token_lookup: a transaction-local
-- setting only the queue's own repository sets (see
-- AIAnalysisJobRepository.withQueueRunner), granting access to this one table. The
-- table holds identifiers, counters and an error string -- no alert content.
create policy queue_runner on ai_analysis_jobs
  using (current_setting('app.ai_queue_runner', true) = 'true')
  with check (current_setting('app.ai_queue_runner', true) = 'true');
