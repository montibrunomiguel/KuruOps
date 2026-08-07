-- ai_analysis_runs backs the agentic "Analyze with AI" tool-use loop
-- (service.AIAnalysisService) -- the piece the backend README long noted
-- was missing: something that actually calls MCPToolService.ProposeToolCall
-- during analysis. A run is a full LLM conversation (messages, in the
-- provider-agnostic internal/llmclient.Message shape) that may span more
-- than one HTTP request: when the model calls a side-effecting tool,
-- MCPToolService leaves that call at 'proposed' and this run goes
-- 'paused' until an analyst approves or rejects it (see
-- ai_tool_calls.status) -- pending_tool_call_id is how the approval action
-- finds its way back to the right run to resume.
create table ai_analysis_runs (
  id                   bigint generated always as identity primary key,
  tenant_id            uuid not null references tenants(id) on delete cascade,
  context_type         text not null,
  context_id           uuid not null,
  actor_id             uuid not null references users(id),
  status               text not null default 'running',
  -- []llmclient.Message, serialized -- the full conversation so far,
  -- replayed to the model on every turn (no server-side prompt caching).
  messages             jsonb not null default '[]',
  -- []llmclient.Tool the run was offered, plus the tool-name -> mcp_server_id
  -- routing resolved at start -- persisted so a resume doesn't need to
  -- re-run tool discovery (and can't drift if the admin's tool config
  -- changes mid-run).
  tools                jsonb not null default '[]',
  tool_routes          jsonb not null default '{}',
  pending_tool_call_id bigint references ai_tool_calls(id),
  result               text,
  error                text,
  created_at           timestamptz not null default now(),
  updated_at           timestamptz not null default now(),
  constraint ai_analysis_runs_context_type_check check (context_type in ('alert', 'incident')),
  constraint ai_analysis_runs_status_check check (status in ('running', 'paused', 'completed', 'failed'))
);

create index ai_analysis_runs_tenant_idx on ai_analysis_runs (tenant_id, created_at desc);
create index ai_analysis_runs_pending_tool_call_idx on ai_analysis_runs (pending_tool_call_id) where status = 'paused';

alter table ai_analysis_runs enable row level security;
alter table ai_analysis_runs force row level security;

create policy tenant_isolation on ai_analysis_runs
  using (tenant_id = current_tenant_id())
  with check (tenant_id = current_tenant_id());
