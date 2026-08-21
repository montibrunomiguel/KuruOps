-- ArgusOps consolidated baseline schema.
--
-- This replaces the 50 incremental migrations that built this schema over
-- the course of development (0001 through 0050) -- squashed into one file
-- because the project has no versioned release yet (see CHANGELOG.md) and
-- no deployment anyone depends on needs the old incremental history
-- replayed step by step. The full prior history (with its per-change
-- rationale) is still in git history if it's ever needed for archaeology.
--
-- Mechanically derived from a fully-migrated database (`pg_dump
-- --schema-only`), not hand-transcribed -- verified by diffing a fresh
-- install against the pre-squash schema, so it is guaranteed to match
-- exactly what the old 50 migrations produced together. Keeps pg_dump's
-- own formatting (uppercase keywords, `public.` schema qualification)
-- rather than this repo's usual lowercase hand-written style, for that
-- same reason: this file's authority comes from being a mechanical,
-- byte-verifiable snapshot, not from being retyped by hand.
--
-- Going forward: treat this file the same as any other shipped migration
-- (see db/migrations/README.md) -- once it's out in a real deployment,
-- don't edit it, land a new migration instead.

-- ============================================================
-- Enum types
-- ============================================================


CREATE EXTENSION IF NOT EXISTS citext WITH SCHEMA public;

CREATE EXTENSION IF NOT EXISTS pgcrypto WITH SCHEMA public;

CREATE TYPE public.actor_type_enum AS ENUM (
    'user',
    'system',
    'ai'
);

CREATE TYPE public.alert_status_enum AS ENUM (
    'open',
    'investigating',
    'escalated',
    'closed'
);

CREATE TYPE public.auth_provider_enum AS ENUM (
    'local',
    'ldap',
    'saml'
);

CREATE TYPE public.classification_enum AS ENUM (
    'false_positive',
    'true_positive',
    'authorized_event'
);

CREATE TYPE public.incident_phase_enum AS ENUM (
    'new',
    'detection_analysis',
    'containment',
    'eradication',
    'recovery',
    'post_incident'
);

CREATE TYPE public.incident_priority_enum AS ENUM (
    'p1',
    'p2',
    'p3',
    'p4'
);

CREATE TYPE public.llm_kind_enum AS ENUM (
    'anthropic',
    'openai_compatible',
    'azure_openai',
    'self_hosted'
);

CREATE TYPE public.mcp_transport_enum AS ENUM (
    'stdio',
    'http',
    'sse'
);

CREATE TYPE public.severity_enum AS ENUM (
    'critical',
    'high',
    'medium',
    'low',
    'informational'
);

CREATE TYPE public.tool_call_status_enum AS ENUM (
    'proposed',
    'approved',
    'rejected',
    'executed',
    'failed'
);


-- ============================================================
-- current_tenant_id(): every row-level-security policy below keys off
-- this. RLS is bypassed by table owners and superusers by default -- the
-- application must connect as a non-owner, non-superuser role
-- (argusops_app) for these policies to have any effect at all. See
-- db/init/*.sql for that role's setup (applied once, after migrations
-- run -- see Taskfile.yml's db:migrate / db:roles).
-- ============================================================

CREATE FUNCTION public.current_tenant_id() RETURNS uuid
    LANGUAGE sql STABLE
    AS $$
  select nullif(current_setting('app.tenant_id', true), '')::uuid
$$;


-- ============================================================
-- Tables (identity-column sequences and each RLS-scoped table's FORCE ROW
-- LEVEL SECURITY are pg_dump's own inline convention, right after the
-- table that owns them; ENABLE ROW LEVEL SECURITY plus the actual
-- policies come later, in their own section, since a policy can only be
-- created once every table it might reference already exists)
-- ============================================================

CREATE TABLE public.ai_analysis_runs (
    id bigint NOT NULL,
    tenant_id uuid NOT NULL,
    context_type text NOT NULL,
    context_id uuid NOT NULL,
    actor_id uuid,
    status text DEFAULT 'running'::text NOT NULL,
    messages jsonb DEFAULT '[]'::jsonb NOT NULL,
    tools jsonb DEFAULT '[]'::jsonb NOT NULL,
    tool_routes jsonb DEFAULT '{}'::jsonb NOT NULL,
    pending_tool_call_id bigint,
    result text,
    error text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT ai_analysis_runs_context_type_check CHECK ((context_type = ANY (ARRAY['alert'::text, 'incident'::text]))),
    CONSTRAINT ai_analysis_runs_status_check CHECK ((status = ANY (ARRAY['running'::text, 'paused'::text, 'completed'::text, 'failed'::text])))
);

ALTER TABLE ONLY public.ai_analysis_runs FORCE ROW LEVEL SECURITY;

ALTER TABLE public.ai_analysis_runs ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.ai_analysis_runs_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.ai_tool_calls (
    id bigint NOT NULL,
    tenant_id uuid NOT NULL,
    mcp_server_id uuid NOT NULL,
    tool_name text NOT NULL,
    context_type text NOT NULL,
    context_id uuid NOT NULL,
    args jsonb DEFAULT '{}'::jsonb NOT NULL,
    result jsonb,
    status public.tool_call_status_enum DEFAULT 'proposed'::public.tool_call_status_enum NOT NULL,
    approved_by uuid,
    approved_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT ai_tool_calls_context_type_check CHECK ((context_type = ANY (ARRAY['alert'::text, 'incident'::text])))
);

ALTER TABLE ONLY public.ai_tool_calls FORCE ROW LEVEL SECURITY;

ALTER TABLE public.ai_tool_calls ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.ai_tool_calls_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.alert_comments (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    alert_id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    author_id uuid NOT NULL,
    author_name text NOT NULL,
    body text NOT NULL,
    attachment_url text,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY public.alert_comments FORCE ROW LEVEL SECURITY;

CREATE TABLE public.alert_events (
    id bigint NOT NULL,
    alert_id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    event_type text NOT NULL,
    actor_type public.actor_type_enum NOT NULL,
    actor_id uuid,
    data jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY public.alert_events FORCE ROW LEVEL SECURITY;

ALTER TABLE public.alert_events ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.alert_events_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.alert_links (
    alert_id uuid NOT NULL,
    linked_alert_id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    created_by uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT alert_links_no_self_link CHECK ((alert_id <> linked_alert_id))
);

ALTER TABLE ONLY public.alert_links FORCE ROW LEVEL SECURITY;

CREATE TABLE public.alerts (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tenant_id uuid NOT NULL,
    external_id text,
    webhook_endpoint_id uuid,
    title text NOT NULL,
    source text NOT NULL,
    severity public.severity_enum NOT NULL,
    original_severity public.severity_enum NOT NULL,
    status public.alert_status_enum DEFAULT 'open'::public.alert_status_enum NOT NULL,
    classification public.classification_enum,
    close_comment text,
    close_attachment_url text,
    rule_id text,
    asset text,
    src_ip inet,
    tags text[] DEFAULT '{}'::text[] NOT NULL,
    payload jsonb NOT NULL,
    incident_id uuid,
    received_at timestamp with time zone DEFAULT now() NOT NULL,
    acknowledged_at timestamp with time zone,
    closed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    assigned_analyst_id uuid,
    escalated_at timestamp with time zone,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    group_key text,
    duplicate_count integer DEFAULT 0 NOT NULL,
    playbook_id uuid,
    sla_escalation_step integer DEFAULT 0 NOT NULL,
    manual_escalation_step integer DEFAULT 0 NOT NULL,
    CONSTRAINT alerts_classification_requires_closed CHECK (((classification IS NULL) OR (status = 'closed'::public.alert_status_enum))),
    CONSTRAINT alerts_closed_requires_classification CHECK (((status <> 'closed'::public.alert_status_enum) OR (classification IS NOT NULL)))
);

ALTER TABLE ONLY public.alerts FORCE ROW LEVEL SECURITY;

CREATE TABLE public.auth_group_mappings (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tenant_id uuid NOT NULL,
    provider public.auth_provider_enum NOT NULL,
    external_group text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    role_id uuid NOT NULL,
    CONSTRAINT auth_group_mappings_provider_check CHECK ((provider = ANY (ARRAY['ldap'::public.auth_provider_enum, 'saml'::public.auth_provider_enum])))
);

ALTER TABLE ONLY public.auth_group_mappings FORCE ROW LEVEL SECURITY;

CREATE TABLE public.escalation_policies (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tenant_id uuid NOT NULL,
    severity public.severity_enum NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY public.escalation_policies FORCE ROW LEVEL SECURITY;

CREATE TABLE public.escalation_policy_steps (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    policy_id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    "position" integer NOT NULL,
    schedule_id uuid NOT NULL,
    delay_minutes integer NOT NULL,
    channel_type text NOT NULL,
    destination_secret_ref text NOT NULL,
    webhook_payload_template text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT escalation_policy_steps_channel_type_check CHECK ((channel_type = ANY (ARRAY['pagerduty'::text, 'slack'::text, 'webhook'::text]))),
    CONSTRAINT escalation_policy_steps_delay_minutes_check CHECK ((delay_minutes > 0))
);

ALTER TABLE ONLY public.escalation_policy_steps FORCE ROW LEVEL SECURITY;

CREATE TABLE public.field_mapping_templates (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tenant_id uuid NOT NULL,
    name text NOT NULL,
    rules jsonb DEFAULT '[]'::jsonb NOT NULL,
    created_by uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY public.field_mapping_templates FORCE ROW LEVEL SECURITY;

CREATE TABLE public.incident_alert_links (
    incident_id uuid NOT NULL,
    alert_id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    linked_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY public.incident_alert_links FORCE ROW LEVEL SECURITY;

CREATE TABLE public.incident_assignees (
    incident_id uuid NOT NULL,
    user_id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY public.incident_assignees FORCE ROW LEVEL SECURITY;

CREATE TABLE public.incident_comments (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    incident_id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    author_id uuid NOT NULL,
    body text NOT NULL,
    attachment_url text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    author_name text NOT NULL
);

ALTER TABLE ONLY public.incident_comments FORCE ROW LEVEL SECURITY;

CREATE TABLE public.incident_events (
    id bigint NOT NULL,
    incident_id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    event_type text NOT NULL,
    actor_type public.actor_type_enum NOT NULL,
    actor_id uuid,
    data jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY public.incident_events FORCE ROW LEVEL SECURITY;

ALTER TABLE public.incident_events ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.incident_events_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.incident_role_assignments (
    incident_id uuid NOT NULL,
    user_id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    role text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT incident_role_assignments_role_check CHECK ((role = ANY (ARRAY['commander'::text, 'incident_handler'::text, 'communications_lead'::text, 'privacy_officer'::text, 'technical_lead'::text])))
);

ALTER TABLE ONLY public.incident_role_assignments FORCE ROW LEVEL SECURITY;

CREATE TABLE public.incident_sla_policies (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tenant_id uuid NOT NULL,
    severity public.severity_enum NOT NULL,
    priority public.incident_priority_enum NOT NULL,
    due_within_minutes integer NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT incident_sla_policies_due_within_minutes_check CHECK ((due_within_minutes > 0))
);

ALTER TABLE ONLY public.incident_sla_policies FORCE ROW LEVEL SECURITY;

CREATE TABLE public.incident_status_history (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    incident_id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    phase public.incident_phase_enum NOT NULL,
    entered_at timestamp with time zone DEFAULT now() NOT NULL,
    corrected_entered_at timestamp with time zone,
    corrected_at timestamp with time zone,
    corrected_by uuid,
    correction_reason text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT incident_status_history_correction_requires_reason CHECK (((corrected_at IS NULL) OR ((correction_reason IS NOT NULL) AND (corrected_entered_at IS NOT NULL))))
);

ALTER TABLE ONLY public.incident_status_history FORCE ROW LEVEL SECURITY;

CREATE TABLE public.incidents (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tenant_id uuid NOT NULL,
    title text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    severity public.severity_enum NOT NULL,
    priority public.incident_priority_enum NOT NULL,
    phase public.incident_phase_enum DEFAULT 'new'::public.incident_phase_enum NOT NULL,
    tags text[] DEFAULT '{}'::text[] NOT NULL,
    sla_due_at timestamp with time zone,
    sla_breached boolean DEFAULT false NOT NULL,
    opened_at timestamp with time zone DEFAULT now() NOT NULL,
    closed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY public.incidents FORCE ROW LEVEL SECURITY;

CREATE TABLE public.llm_providers (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tenant_id uuid NOT NULL,
    name text NOT NULL,
    kind public.llm_kind_enum NOT NULL,
    base_url text,
    model text NOT NULL,
    api_key_secret_ref text NOT NULL,
    is_default boolean DEFAULT false NOT NULL,
    created_by uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    auto_analyze_all_alerts boolean DEFAULT false NOT NULL
);

ALTER TABLE ONLY public.llm_providers FORCE ROW LEVEL SECURITY;

CREATE TABLE public.mcp_servers (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tenant_id uuid NOT NULL,
    name text NOT NULL,
    transport public.mcp_transport_enum NOT NULL,
    endpoint_or_command text NOT NULL,
    auth_secret_ref text,
    allowed_tools text[] DEFAULT '{}'::text[] NOT NULL,
    enabled_for text[] DEFAULT '{}'::text[] NOT NULL,
    side_effecting_tools text[] DEFAULT '{}'::text[] NOT NULL,
    is_enabled boolean DEFAULT true NOT NULL,
    created_by uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY public.mcp_servers FORCE ROW LEVEL SECURITY;

-- These three intentionally do NOT end in `WITH NO DATA` (what pg_dump
-- would normally emit for a materialized view, since a schema-only dump
-- never carries data, and Postgres has to represent "not yet populated"
-- explicitly somehow). cmd/worker's periodic refresh always runs `REFRESH
-- MATERIALIZED VIEW CONCURRENTLY`, which requires the view to already be
-- populated at least once -- a fresh install would otherwise fail every
-- dashboard/stats query until someone manually ran a plain
-- (non-CONCURRENTLY) refresh. Letting each CREATE run its query
-- immediately (empty result set on a fresh install, same as it always was
-- pre-squash) keeps that first refresh working out of the box.
CREATE MATERIALIZED VIEW public.mv_alert_daily_stats AS
 SELECT tenant_id,
    date_trunc('day'::text, received_at) AS day,
    count(*) AS alert_count,
    count(*) FILTER (WHERE (severity = 'critical'::public.severity_enum)) AS critical_count,
    avg(EXTRACT(epoch FROM (acknowledged_at - received_at))) FILTER (WHERE (acknowledged_at IS NOT NULL)) AS avg_mtta_seconds,
    avg(EXTRACT(epoch FROM (closed_at - received_at))) FILTER (WHERE (closed_at IS NOT NULL)) AS avg_mttr_seconds
   FROM public.alerts
  GROUP BY tenant_id, (date_trunc('day'::text, received_at));

CREATE MATERIALIZED VIEW public.mv_incident_daily_stats AS
 SELECT tenant_id,
    date_trunc('day'::text, opened_at) AS day,
    count(*) AS incident_count
   FROM public.incidents
  GROUP BY tenant_id, (date_trunc('day'::text, opened_at));

CREATE MATERIALIZED VIEW public.mv_incident_kpis AS
 SELECT tenant_id,
    count(*) FILTER (WHERE (phase <> 'post_incident'::public.incident_phase_enum)) AS active_incidents,
    count(*) FILTER (WHERE sla_breached) AS sla_breached_count,
    count(*) FILTER (WHERE ((priority = 'p1'::public.incident_priority_enum) AND (phase <> 'post_incident'::public.incident_phase_enum))) AS p1_open_count,
    avg(EXTRACT(epoch FROM (( SELECT min(COALESCE(h.corrected_entered_at, h.entered_at)) AS min
           FROM public.incident_status_history h
          WHERE ((h.incident_id = i.id) AND (h.phase = 'detection_analysis'::public.incident_phase_enum))) - opened_at))) AS avg_mtta_seconds,
    avg(EXTRACT(epoch FROM (( SELECT min(COALESCE(h.corrected_entered_at, h.entered_at)) AS min
           FROM public.incident_status_history h
          WHERE ((h.incident_id = i.id) AND (h.phase = 'post_incident'::public.incident_phase_enum))) - opened_at))) AS avg_mttr_seconds
   FROM public.incidents i
  GROUP BY tenant_id;

CREATE TABLE public.on_call_overrides (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    schedule_id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    user_id uuid NOT NULL,
    override_date date NOT NULL,
    created_by uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY public.on_call_overrides FORCE ROW LEVEL SECURITY;

CREATE TABLE public.on_call_rotation_participants (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    schedule_id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    user_id uuid NOT NULL,
    "position" integer NOT NULL
);

ALTER TABLE ONLY public.on_call_rotation_participants FORCE ROW LEVEL SECURITY;

CREATE TABLE public.on_call_schedules (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tenant_id uuid NOT NULL,
    name text DEFAULT 'Primary On-Call'::text NOT NULL,
    handover_at timestamp with time zone DEFAULT now() NOT NULL,
    period_days integer DEFAULT 7 NOT NULL,
    concurrent_shifts integer DEFAULT 1 NOT NULL,
    working_hours_mode text DEFAULT 'all_day'::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    is_default boolean DEFAULT false NOT NULL,
    CONSTRAINT on_call_schedules_concurrent_shifts_check CHECK ((concurrent_shifts > 0)),
    CONSTRAINT on_call_schedules_period_days_check CHECK ((period_days > 0)),
    CONSTRAINT on_call_schedules_working_hours_mode_check CHECK ((working_hours_mode = ANY (ARRAY['all_day'::text, 'specific_times'::text])))
);

ALTER TABLE ONLY public.on_call_schedules FORCE ROW LEVEL SECURITY;

CREATE TABLE public.on_call_working_hours (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    schedule_id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    weekdays smallint[] NOT NULL,
    start_minute smallint NOT NULL,
    end_minute smallint NOT NULL,
    CONSTRAINT on_call_working_hours_end_minute_check CHECK (((end_minute >= 0) AND (end_minute <= 1439))),
    CONSTRAINT on_call_working_hours_start_minute_check CHECK (((start_minute >= 0) AND (start_minute <= 1439)))
);

ALTER TABLE ONLY public.on_call_working_hours FORCE ROW LEVEL SECURITY;

CREATE TABLE public.password_reset_tokens (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tenant_id uuid NOT NULL,
    user_id uuid NOT NULL,
    token_hash text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    used_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY public.password_reset_tokens FORCE ROW LEVEL SECURITY;

CREATE TABLE public.playbook_phase_steps (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    playbook_id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    phase public.incident_phase_enum NOT NULL,
    step_order integer NOT NULL,
    action_text text NOT NULL,
    webhook_url text DEFAULT ''::text NOT NULL,
    webhook_payload_template text DEFAULT ''::text NOT NULL
);

ALTER TABLE ONLY public.playbook_phase_steps FORCE ROW LEVEL SECURITY;

CREATE TABLE public.playbooks (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tenant_id uuid NOT NULL,
    title text NOT NULL,
    category text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    keywords text[] DEFAULT '{}'::text[] NOT NULL,
    created_by uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    alert_name_pattern text DEFAULT ''::text NOT NULL,
    is_default boolean DEFAULT false NOT NULL
);

ALTER TABLE ONLY public.playbooks FORCE ROW LEVEL SECURITY;

CREATE TABLE public.rate_limit_events (
    scope text NOT NULL,
    key text NOT NULL,
    occurred_at timestamp with time zone DEFAULT now() NOT NULL,
    id bigint NOT NULL
);

ALTER TABLE public.rate_limit_events ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.rate_limit_events_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.refresh_tokens (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tenant_id uuid NOT NULL,
    user_id uuid NOT NULL,
    token_hash text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    revoked_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY public.refresh_tokens FORCE ROW LEVEL SECURITY;

CREATE TABLE public.roles (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tenant_id uuid NOT NULL,
    name text NOT NULL,
    is_admin boolean DEFAULT false NOT NULL,
    resource_access text[] DEFAULT '{}'::text[] NOT NULL,
    allowed_tags text[] DEFAULT '{}'::text[] NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT roles_resource_access_valid CHECK ((resource_access <@ ARRAY['alerts'::text, 'incidents'::text, 'followup'::text]))
);

ALTER TABLE ONLY public.roles FORCE ROW LEVEL SECURITY;

CREATE TABLE public.secret_store (
    ref text NOT NULL,
    nonce bytea NOT NULL,
    ciphertext bytea NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE public.tags (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tenant_id uuid NOT NULL,
    name text NOT NULL,
    color text,
    created_by uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY public.tags FORCE ROW LEVEL SECURITY;

CREATE TABLE public.tenant_ldap_config (
    tenant_id uuid NOT NULL,
    host text NOT NULL,
    port integer DEFAULT 636 NOT NULL,
    use_tls boolean DEFAULT true NOT NULL,
    bind_dn text NOT NULL,
    bind_password_secret_ref text NOT NULL,
    user_base_dn text NOT NULL,
    user_filter text DEFAULT '(mail=%s)'::text NOT NULL,
    group_base_dn text,
    group_attribute text DEFAULT 'memberOf'::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY public.tenant_ldap_config FORCE ROW LEVEL SECURITY;

CREATE TABLE public.tenant_saml_config (
    tenant_id uuid NOT NULL,
    idp_metadata_url text,
    idp_metadata_xml text,
    sp_entity_id text NOT NULL,
    acs_url text NOT NULL,
    sp_cert_secret_ref text NOT NULL,
    sp_key_secret_ref text NOT NULL,
    group_attribute text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT tenant_saml_config_metadata_source CHECK (((idp_metadata_url IS NOT NULL) OR (idp_metadata_xml IS NOT NULL)))
);

ALTER TABLE ONLY public.tenant_saml_config FORCE ROW LEVEL SECURITY;

CREATE TABLE public.tenant_smtp_config (
    tenant_id uuid NOT NULL,
    host text NOT NULL,
    port integer DEFAULT 587 NOT NULL,
    use_tls boolean DEFAULT true NOT NULL,
    username text NOT NULL,
    password_secret_ref text NOT NULL,
    from_address text NOT NULL,
    from_name text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY public.tenant_smtp_config FORCE ROW LEVEL SECURITY;

CREATE TABLE public.tenant_storage_config (
    tenant_id uuid NOT NULL,
    provider text NOT NULL,
    s3_bucket text,
    s3_region text,
    s3_access_key_id text,
    s3_secret_access_key_secret_ref text,
    gcs_bucket text,
    gcs_project_id text,
    gcs_credentials_json_secret_ref text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT tenant_storage_config_provider_check CHECK ((provider = ANY (ARRAY['s3'::text, 'gcs'::text]))),
    CONSTRAINT tenant_storage_config_provider_fields CHECK ((((provider = 's3'::text) AND (s3_bucket IS NOT NULL) AND (s3_region IS NOT NULL) AND (s3_access_key_id IS NOT NULL) AND (s3_secret_access_key_secret_ref IS NOT NULL)) OR ((provider = 'gcs'::text) AND (gcs_bucket IS NOT NULL) AND (gcs_project_id IS NOT NULL) AND (gcs_credentials_json_secret_ref IS NOT NULL))))
);

ALTER TABLE ONLY public.tenant_storage_config FORCE ROW LEVEL SECURITY;

CREATE TABLE public.tenants (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name text NOT NULL,
    slug text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    timezone text DEFAULT 'UTC'::text NOT NULL
);

CREATE TABLE public.upload_keys (
    key text NOT NULL,
    tenant_id uuid NOT NULL,
    context_type text NOT NULL,
    context_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT upload_keys_context_type_check CHECK ((context_type = ANY (ARRAY['alert'::text, 'incident'::text])))
);

ALTER TABLE ONLY public.upload_keys FORCE ROW LEVEL SECURITY;

CREATE TABLE public.user_api_tokens (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tenant_id uuid NOT NULL,
    user_id uuid NOT NULL,
    name text NOT NULL,
    token_hash text NOT NULL,
    token_last4 text NOT NULL,
    expires_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    revoked_at timestamp with time zone
);

ALTER TABLE ONLY public.user_api_tokens FORCE ROW LEVEL SECURITY;

CREATE TABLE public.users (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tenant_id uuid NOT NULL,
    email public.citext NOT NULL,
    name text NOT NULL,
    auth_provider public.auth_provider_enum DEFAULT 'local'::public.auth_provider_enum NOT NULL,
    external_id text,
    password_hash text,
    mfa_totp_secret text,
    is_active boolean DEFAULT true NOT NULL,
    last_login_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    must_change_password boolean DEFAULT false NOT NULL,
    role_id uuid NOT NULL,
    phone text,
    CONSTRAINT users_federated_requires_external_id CHECK (((auth_provider = 'local'::public.auth_provider_enum) OR (external_id IS NOT NULL))),
    CONSTRAINT users_local_requires_password CHECK (((auth_provider <> 'local'::public.auth_provider_enum) OR (password_hash IS NOT NULL)))
);

ALTER TABLE ONLY public.users FORCE ROW LEVEL SECURITY;

CREATE TABLE public.webhook_endpoints (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tenant_id uuid NOT NULL,
    name text NOT NULL,
    source text NOT NULL,
    token_hash text NOT NULL,
    token_last4 text NOT NULL,
    status text DEFAULT 'active'::text NOT NULL,
    rotated_at timestamp with time zone,
    created_by uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    expires_at timestamp with time zone,
    field_mapping_template_id uuid,
    group_by_fields text[] DEFAULT '{}'::text[] NOT NULL,
    dedup_window_minutes integer DEFAULT 30 NOT NULL,
    CONSTRAINT webhook_endpoints_status_check CHECK ((status = ANY (ARRAY['active'::text, 'disabled'::text])))
);

ALTER TABLE ONLY public.webhook_endpoints FORCE ROW LEVEL SECURITY;


-- ============================================================
-- Primary keys & unique constraints
-- ============================================================

ALTER TABLE ONLY public.ai_analysis_runs
    ADD CONSTRAINT ai_analysis_runs_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.ai_tool_calls
    ADD CONSTRAINT ai_tool_calls_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.alert_comments
    ADD CONSTRAINT alert_comments_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.alert_events
    ADD CONSTRAINT alert_events_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.alert_links
    ADD CONSTRAINT alert_links_pkey PRIMARY KEY (alert_id, linked_alert_id);

ALTER TABLE ONLY public.alerts
    ADD CONSTRAINT alerts_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.auth_group_mappings
    ADD CONSTRAINT auth_group_mappings_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.escalation_policies
    ADD CONSTRAINT escalation_policies_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.escalation_policies
    ADD CONSTRAINT escalation_policies_tenant_id_severity_key UNIQUE (tenant_id, severity);

ALTER TABLE ONLY public.escalation_policy_steps
    ADD CONSTRAINT escalation_policy_steps_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.escalation_policy_steps
    ADD CONSTRAINT escalation_policy_steps_policy_id_position_key UNIQUE (policy_id, "position");

ALTER TABLE ONLY public.field_mapping_templates
    ADD CONSTRAINT field_mapping_templates_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.incident_alert_links
    ADD CONSTRAINT incident_alert_links_pkey PRIMARY KEY (incident_id, alert_id);

ALTER TABLE ONLY public.incident_assignees
    ADD CONSTRAINT incident_assignees_pkey PRIMARY KEY (incident_id, user_id);

ALTER TABLE ONLY public.incident_comments
    ADD CONSTRAINT incident_comments_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.incident_events
    ADD CONSTRAINT incident_events_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.incident_role_assignments
    ADD CONSTRAINT incident_role_assignments_pkey PRIMARY KEY (incident_id, user_id, role);

ALTER TABLE ONLY public.incident_sla_policies
    ADD CONSTRAINT incident_sla_policies_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.incident_sla_policies
    ADD CONSTRAINT incident_sla_policies_tenant_id_severity_priority_key UNIQUE (tenant_id, severity, priority);

ALTER TABLE ONLY public.incident_status_history
    ADD CONSTRAINT incident_status_history_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.incidents
    ADD CONSTRAINT incidents_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.llm_providers
    ADD CONSTRAINT llm_providers_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.mcp_servers
    ADD CONSTRAINT mcp_servers_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.on_call_overrides
    ADD CONSTRAINT on_call_overrides_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.on_call_rotation_participants
    ADD CONSTRAINT on_call_rotation_participants_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.on_call_schedules
    ADD CONSTRAINT on_call_schedules_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.on_call_working_hours
    ADD CONSTRAINT on_call_working_hours_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.password_reset_tokens
    ADD CONSTRAINT password_reset_tokens_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.password_reset_tokens
    ADD CONSTRAINT password_reset_tokens_token_hash_key UNIQUE (token_hash);

ALTER TABLE ONLY public.playbook_phase_steps
    ADD CONSTRAINT playbook_phase_steps_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.playbooks
    ADD CONSTRAINT playbooks_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.rate_limit_events
    ADD CONSTRAINT rate_limit_events_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.refresh_tokens
    ADD CONSTRAINT refresh_tokens_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.refresh_tokens
    ADD CONSTRAINT refresh_tokens_token_hash_key UNIQUE (token_hash);

ALTER TABLE ONLY public.roles
    ADD CONSTRAINT roles_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.roles
    ADD CONSTRAINT roles_tenant_id_name_key UNIQUE (tenant_id, name);

ALTER TABLE ONLY public.secret_store
    ADD CONSTRAINT secret_store_pkey PRIMARY KEY (ref);

ALTER TABLE ONLY public.tags
    ADD CONSTRAINT tags_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.tenant_ldap_config
    ADD CONSTRAINT tenant_ldap_config_pkey PRIMARY KEY (tenant_id);

ALTER TABLE ONLY public.tenant_saml_config
    ADD CONSTRAINT tenant_saml_config_pkey PRIMARY KEY (tenant_id);

ALTER TABLE ONLY public.tenant_smtp_config
    ADD CONSTRAINT tenant_smtp_config_pkey PRIMARY KEY (tenant_id);

ALTER TABLE ONLY public.tenant_storage_config
    ADD CONSTRAINT tenant_storage_config_pkey PRIMARY KEY (tenant_id);

ALTER TABLE ONLY public.tenants
    ADD CONSTRAINT tenants_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.tenants
    ADD CONSTRAINT tenants_slug_key UNIQUE (slug);

ALTER TABLE ONLY public.upload_keys
    ADD CONSTRAINT upload_keys_pkey PRIMARY KEY (key);

ALTER TABLE ONLY public.user_api_tokens
    ADD CONSTRAINT user_api_tokens_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.user_api_tokens
    ADD CONSTRAINT user_api_tokens_token_hash_key UNIQUE (token_hash);

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.webhook_endpoints
    ADD CONSTRAINT webhook_endpoints_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.webhook_endpoints
    ADD CONSTRAINT webhook_endpoints_token_hash_key UNIQUE (token_hash);


-- ============================================================
-- Indexes
-- ============================================================

CREATE INDEX ai_analysis_runs_pending_tool_call_idx ON public.ai_analysis_runs USING btree (pending_tool_call_id) WHERE (status = 'paused'::text);

CREATE INDEX ai_analysis_runs_tenant_idx ON public.ai_analysis_runs USING btree (tenant_id, created_at DESC);

CREATE INDEX ai_tool_calls_context_idx ON public.ai_tool_calls USING btree (context_type, context_id);

CREATE INDEX ai_tool_calls_mcp_server_idx ON public.ai_tool_calls USING btree (mcp_server_id);

CREATE INDEX ai_tool_calls_pending_idx ON public.ai_tool_calls USING btree (tenant_id) WHERE (status = 'proposed'::public.tool_call_status_enum);

CREATE INDEX ai_tool_calls_tenant_idx ON public.ai_tool_calls USING btree (tenant_id, created_at DESC);

CREATE INDEX alert_comments_alert_id_idx ON public.alert_comments USING btree (alert_id, created_at);

CREATE INDEX alert_events_alert_id_idx ON public.alert_events USING btree (alert_id, created_at);

CREATE INDEX alert_events_tenant_idx ON public.alert_events USING btree (tenant_id, created_at DESC);

CREATE INDEX alerts_assigned_analyst_idx ON public.alerts USING btree (assigned_analyst_id) WHERE (assigned_analyst_id IS NOT NULL);

CREATE INDEX alerts_dedup_lookup_idx ON public.alerts USING btree (webhook_endpoint_id, group_key, received_at) WHERE (group_key IS NOT NULL);

CREATE INDEX alerts_incident_id_idx ON public.alerts USING btree (incident_id);

CREATE INDEX alerts_payload_gin ON public.alerts USING gin (payload jsonb_path_ops);

CREATE INDEX alerts_playbook_id_idx ON public.alerts USING btree (playbook_id) WHERE (playbook_id IS NOT NULL);

CREATE UNIQUE INDEX alerts_tenant_external_id_uq ON public.alerts USING btree (tenant_id, source, external_id) WHERE (external_id IS NOT NULL);

CREATE INDEX alerts_tenant_received_idx ON public.alerts USING btree (tenant_id, received_at DESC);

CREATE INDEX alerts_tenant_status_idx ON public.alerts USING btree (tenant_id, status);

CREATE INDEX alerts_tenant_tags_gin ON public.alerts USING gin (tags);

CREATE UNIQUE INDEX auth_group_mappings_uq ON public.auth_group_mappings USING btree (tenant_id, provider, external_group);

CREATE INDEX escalation_policy_steps_schedule_idx ON public.escalation_policy_steps USING btree (schedule_id);

CREATE UNIQUE INDEX field_mapping_templates_tenant_name_uq ON public.field_mapping_templates USING btree (tenant_id, lower(name));

CREATE INDEX incident_alert_links_alert_id_idx ON public.incident_alert_links USING btree (alert_id);

CREATE INDEX incident_assignees_incident_idx ON public.incident_assignees USING btree (incident_id);

CREATE INDEX incident_assignees_user_idx ON public.incident_assignees USING btree (user_id);

CREATE INDEX incident_comments_incident_id_idx ON public.incident_comments USING btree (incident_id, created_at);

CREATE INDEX incident_events_incident_id_idx ON public.incident_events USING btree (incident_id, created_at);

CREATE INDEX incident_events_tenant_idx ON public.incident_events USING btree (tenant_id, created_at DESC);

CREATE INDEX incident_role_assignments_incident_idx ON public.incident_role_assignments USING btree (incident_id);

CREATE UNIQUE INDEX incident_role_assignments_single_commander ON public.incident_role_assignments USING btree (incident_id) WHERE (role = 'commander'::text);

CREATE UNIQUE INDEX incident_role_assignments_single_technical_lead ON public.incident_role_assignments USING btree (incident_id) WHERE (role = 'technical_lead'::text);

CREATE UNIQUE INDEX incident_status_history_phase_uq ON public.incident_status_history USING btree (incident_id, phase);

CREATE INDEX incidents_tenant_opened_idx ON public.incidents USING btree (tenant_id, opened_at DESC);

CREATE INDEX incidents_tenant_phase_idx ON public.incidents USING btree (tenant_id, phase);

CREATE INDEX incidents_tenant_tags_gin ON public.incidents USING gin (tags);

CREATE UNIQUE INDEX llm_providers_one_default_per_tenant ON public.llm_providers USING btree (tenant_id) WHERE is_default;

CREATE UNIQUE INDEX llm_providers_tenant_name_uq ON public.llm_providers USING btree (tenant_id, name);

CREATE UNIQUE INDEX mcp_servers_tenant_name_uq ON public.mcp_servers USING btree (tenant_id, name);

CREATE UNIQUE INDEX mv_alert_daily_stats_uq ON public.mv_alert_daily_stats USING btree (tenant_id, day);

CREATE UNIQUE INDEX mv_incident_daily_stats_uq ON public.mv_incident_daily_stats USING btree (tenant_id, day);

CREATE UNIQUE INDEX mv_incident_kpis_uq ON public.mv_incident_kpis USING btree (tenant_id);

CREATE UNIQUE INDEX on_call_overrides_date_uq ON public.on_call_overrides USING btree (schedule_id, override_date);

CREATE UNIQUE INDEX on_call_participants_order_uq ON public.on_call_rotation_participants USING btree (schedule_id, "position");

CREATE INDEX on_call_rotation_participants_user_idx ON public.on_call_rotation_participants USING btree (user_id);

CREATE UNIQUE INDEX on_call_schedules_one_default_per_tenant ON public.on_call_schedules USING btree (tenant_id) WHERE is_default;

CREATE INDEX password_reset_tokens_user_idx ON public.password_reset_tokens USING btree (user_id);

CREATE UNIQUE INDEX playbook_phase_steps_order_uq ON public.playbook_phase_steps USING btree (playbook_id, phase, step_order);

CREATE UNIQUE INDEX playbooks_one_default_per_tenant ON public.playbooks USING btree (tenant_id) WHERE is_default;

CREATE INDEX playbooks_tenant_keywords_gin ON public.playbooks USING gin (keywords);

CREATE INDEX rate_limit_events_scope_key_occurred_at_idx ON public.rate_limit_events USING btree (scope, key, occurred_at);

CREATE INDEX rate_limit_events_scope_occurred_at_idx ON public.rate_limit_events USING btree (scope, occurred_at);

CREATE INDEX refresh_tokens_user_idx ON public.refresh_tokens USING btree (user_id);

CREATE UNIQUE INDEX tags_tenant_name_uq ON public.tags USING btree (tenant_id, lower(name));

CREATE INDEX upload_keys_tenant_id_key_idx ON public.upload_keys USING btree (tenant_id, key);

CREATE INDEX user_api_tokens_user_id_idx ON public.user_api_tokens USING btree (user_id);

CREATE INDEX users_external_id_idx ON public.users USING btree (tenant_id, auth_provider, external_id);

CREATE UNIQUE INDEX users_tenant_email_uq ON public.users USING btree (tenant_id, email);

CREATE INDEX users_tenant_id_idx ON public.users USING btree (tenant_id);

CREATE INDEX webhook_endpoints_field_mapping_template_id_idx ON public.webhook_endpoints USING btree (field_mapping_template_id);

CREATE UNIQUE INDEX webhook_endpoints_tenant_name_uq ON public.webhook_endpoints USING btree (tenant_id, name);


-- ============================================================
-- Foreign keys
-- ============================================================

ALTER TABLE ONLY public.ai_analysis_runs
    ADD CONSTRAINT ai_analysis_runs_actor_id_fkey FOREIGN KEY (actor_id) REFERENCES public.users(id);

ALTER TABLE ONLY public.ai_analysis_runs
    ADD CONSTRAINT ai_analysis_runs_pending_tool_call_id_fkey FOREIGN KEY (pending_tool_call_id) REFERENCES public.ai_tool_calls(id);

ALTER TABLE ONLY public.ai_analysis_runs
    ADD CONSTRAINT ai_analysis_runs_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.ai_tool_calls
    ADD CONSTRAINT ai_tool_calls_approved_by_fkey FOREIGN KEY (approved_by) REFERENCES public.users(id);

ALTER TABLE ONLY public.ai_tool_calls
    ADD CONSTRAINT ai_tool_calls_mcp_server_id_fkey FOREIGN KEY (mcp_server_id) REFERENCES public.mcp_servers(id);

ALTER TABLE ONLY public.ai_tool_calls
    ADD CONSTRAINT ai_tool_calls_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.alert_comments
    ADD CONSTRAINT alert_comments_alert_id_fkey FOREIGN KEY (alert_id) REFERENCES public.alerts(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.alert_comments
    ADD CONSTRAINT alert_comments_author_id_fkey FOREIGN KEY (author_id) REFERENCES public.users(id);

ALTER TABLE ONLY public.alert_comments
    ADD CONSTRAINT alert_comments_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.alert_events
    ADD CONSTRAINT alert_events_actor_id_fkey FOREIGN KEY (actor_id) REFERENCES public.users(id);

ALTER TABLE ONLY public.alert_events
    ADD CONSTRAINT alert_events_alert_id_fkey FOREIGN KEY (alert_id) REFERENCES public.alerts(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.alert_events
    ADD CONSTRAINT alert_events_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.alert_links
    ADD CONSTRAINT alert_links_alert_id_fkey FOREIGN KEY (alert_id) REFERENCES public.alerts(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.alert_links
    ADD CONSTRAINT alert_links_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id);

ALTER TABLE ONLY public.alert_links
    ADD CONSTRAINT alert_links_linked_alert_id_fkey FOREIGN KEY (linked_alert_id) REFERENCES public.alerts(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.alert_links
    ADD CONSTRAINT alert_links_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.alerts
    ADD CONSTRAINT alerts_assigned_analyst_id_fkey FOREIGN KEY (assigned_analyst_id) REFERENCES public.users(id);

ALTER TABLE ONLY public.alerts
    ADD CONSTRAINT alerts_incident_id_fkey FOREIGN KEY (incident_id) REFERENCES public.incidents(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.alerts
    ADD CONSTRAINT alerts_playbook_id_fkey FOREIGN KEY (playbook_id) REFERENCES public.playbooks(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.alerts
    ADD CONSTRAINT alerts_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.alerts
    ADD CONSTRAINT alerts_webhook_endpoint_id_fkey FOREIGN KEY (webhook_endpoint_id) REFERENCES public.webhook_endpoints(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.auth_group_mappings
    ADD CONSTRAINT auth_group_mappings_role_id_fkey FOREIGN KEY (role_id) REFERENCES public.roles(id);

ALTER TABLE ONLY public.auth_group_mappings
    ADD CONSTRAINT auth_group_mappings_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.escalation_policies
    ADD CONSTRAINT escalation_policies_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.escalation_policy_steps
    ADD CONSTRAINT escalation_policy_steps_policy_id_fkey FOREIGN KEY (policy_id) REFERENCES public.escalation_policies(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.escalation_policy_steps
    ADD CONSTRAINT escalation_policy_steps_schedule_id_fkey FOREIGN KEY (schedule_id) REFERENCES public.on_call_schedules(id);

ALTER TABLE ONLY public.escalation_policy_steps
    ADD CONSTRAINT escalation_policy_steps_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.field_mapping_templates
    ADD CONSTRAINT field_mapping_templates_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id);

ALTER TABLE ONLY public.field_mapping_templates
    ADD CONSTRAINT field_mapping_templates_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.incident_alert_links
    ADD CONSTRAINT incident_alert_links_alert_id_fkey FOREIGN KEY (alert_id) REFERENCES public.alerts(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.incident_alert_links
    ADD CONSTRAINT incident_alert_links_incident_id_fkey FOREIGN KEY (incident_id) REFERENCES public.incidents(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.incident_alert_links
    ADD CONSTRAINT incident_alert_links_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.incident_assignees
    ADD CONSTRAINT incident_assignees_incident_id_fkey FOREIGN KEY (incident_id) REFERENCES public.incidents(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.incident_assignees
    ADD CONSTRAINT incident_assignees_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.incident_assignees
    ADD CONSTRAINT incident_assignees_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id);

ALTER TABLE ONLY public.incident_comments
    ADD CONSTRAINT incident_comments_author_id_fkey FOREIGN KEY (author_id) REFERENCES public.users(id);

ALTER TABLE ONLY public.incident_comments
    ADD CONSTRAINT incident_comments_incident_id_fkey FOREIGN KEY (incident_id) REFERENCES public.incidents(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.incident_comments
    ADD CONSTRAINT incident_comments_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.incident_events
    ADD CONSTRAINT incident_events_actor_id_fkey FOREIGN KEY (actor_id) REFERENCES public.users(id);

ALTER TABLE ONLY public.incident_events
    ADD CONSTRAINT incident_events_incident_id_fkey FOREIGN KEY (incident_id) REFERENCES public.incidents(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.incident_events
    ADD CONSTRAINT incident_events_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.incident_role_assignments
    ADD CONSTRAINT incident_role_assignments_incident_id_fkey FOREIGN KEY (incident_id) REFERENCES public.incidents(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.incident_role_assignments
    ADD CONSTRAINT incident_role_assignments_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.incident_role_assignments
    ADD CONSTRAINT incident_role_assignments_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id);

ALTER TABLE ONLY public.incident_sla_policies
    ADD CONSTRAINT incident_sla_policies_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.incident_status_history
    ADD CONSTRAINT incident_status_history_corrected_by_fkey FOREIGN KEY (corrected_by) REFERENCES public.users(id);

ALTER TABLE ONLY public.incident_status_history
    ADD CONSTRAINT incident_status_history_incident_id_fkey FOREIGN KEY (incident_id) REFERENCES public.incidents(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.incident_status_history
    ADD CONSTRAINT incident_status_history_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.incidents
    ADD CONSTRAINT incidents_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.llm_providers
    ADD CONSTRAINT llm_providers_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id);

ALTER TABLE ONLY public.llm_providers
    ADD CONSTRAINT llm_providers_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.mcp_servers
    ADD CONSTRAINT mcp_servers_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id);

ALTER TABLE ONLY public.mcp_servers
    ADD CONSTRAINT mcp_servers_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.on_call_overrides
    ADD CONSTRAINT on_call_overrides_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id);

ALTER TABLE ONLY public.on_call_overrides
    ADD CONSTRAINT on_call_overrides_schedule_id_fkey FOREIGN KEY (schedule_id) REFERENCES public.on_call_schedules(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.on_call_overrides
    ADD CONSTRAINT on_call_overrides_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.on_call_overrides
    ADD CONSTRAINT on_call_overrides_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id);

ALTER TABLE ONLY public.on_call_rotation_participants
    ADD CONSTRAINT on_call_rotation_participants_schedule_id_fkey FOREIGN KEY (schedule_id) REFERENCES public.on_call_schedules(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.on_call_rotation_participants
    ADD CONSTRAINT on_call_rotation_participants_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.on_call_rotation_participants
    ADD CONSTRAINT on_call_rotation_participants_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id);

ALTER TABLE ONLY public.on_call_schedules
    ADD CONSTRAINT on_call_schedules_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.on_call_working_hours
    ADD CONSTRAINT on_call_working_hours_schedule_id_fkey FOREIGN KEY (schedule_id) REFERENCES public.on_call_schedules(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.on_call_working_hours
    ADD CONSTRAINT on_call_working_hours_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.password_reset_tokens
    ADD CONSTRAINT password_reset_tokens_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.password_reset_tokens
    ADD CONSTRAINT password_reset_tokens_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.playbook_phase_steps
    ADD CONSTRAINT playbook_phase_steps_playbook_id_fkey FOREIGN KEY (playbook_id) REFERENCES public.playbooks(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.playbook_phase_steps
    ADD CONSTRAINT playbook_phase_steps_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.playbooks
    ADD CONSTRAINT playbooks_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id);

ALTER TABLE ONLY public.playbooks
    ADD CONSTRAINT playbooks_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.refresh_tokens
    ADD CONSTRAINT refresh_tokens_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.refresh_tokens
    ADD CONSTRAINT refresh_tokens_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.roles
    ADD CONSTRAINT roles_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.tags
    ADD CONSTRAINT tags_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id);

ALTER TABLE ONLY public.tags
    ADD CONSTRAINT tags_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.tenant_ldap_config
    ADD CONSTRAINT tenant_ldap_config_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.tenant_saml_config
    ADD CONSTRAINT tenant_saml_config_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.tenant_smtp_config
    ADD CONSTRAINT tenant_smtp_config_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.tenant_storage_config
    ADD CONSTRAINT tenant_storage_config_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.upload_keys
    ADD CONSTRAINT upload_keys_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.user_api_tokens
    ADD CONSTRAINT user_api_tokens_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.user_api_tokens
    ADD CONSTRAINT user_api_tokens_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_role_id_fkey FOREIGN KEY (role_id) REFERENCES public.roles(id);

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.webhook_endpoints
    ADD CONSTRAINT webhook_endpoints_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id);

ALTER TABLE ONLY public.webhook_endpoints
    ADD CONSTRAINT webhook_endpoints_field_mapping_template_id_fkey FOREIGN KEY (field_mapping_template_id) REFERENCES public.field_mapping_templates(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.webhook_endpoints
    ADD CONSTRAINT webhook_endpoints_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;


-- ============================================================
-- Row-level security: tenant_isolation on every tenant-scoped table
-- (using + with check tenant_id = current_tenant_id()), plus two narrow
-- SELECT-only carve-outs -- webhook_token_lookup and api_token_lookup --
-- for the pre-tenant-context token lookups that happen before
-- app.tenant_id can be set (see WebhookRepository.ResolveToken /
-- UserAPITokenRepository.ResolveToken: they set a one-shot
-- app.webhook_lookup / app.api_token_lookup session flag instead, scoped
-- to exactly that one lookup query).
-- ============================================================

ALTER TABLE public.ai_analysis_runs ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.ai_tool_calls ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.alert_comments ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.alert_events ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.alert_links ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.alerts ENABLE ROW LEVEL SECURITY;

CREATE POLICY api_token_lookup ON public.user_api_tokens FOR SELECT USING ((current_setting('app.api_token_lookup'::text, true) = 'true'::text));

ALTER TABLE public.auth_group_mappings ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.escalation_policies ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.escalation_policy_steps ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.field_mapping_templates ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.incident_alert_links ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.incident_assignees ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.incident_comments ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.incident_events ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.incident_role_assignments ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.incident_sla_policies ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.incident_status_history ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.incidents ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.llm_providers ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.mcp_servers ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.on_call_overrides ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.on_call_rotation_participants ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.on_call_schedules ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.on_call_working_hours ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.password_reset_tokens ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.playbook_phase_steps ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.playbooks ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.refresh_tokens ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.roles ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.tags ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON public.ai_analysis_runs USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

CREATE POLICY tenant_isolation ON public.ai_tool_calls USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

CREATE POLICY tenant_isolation ON public.alert_comments USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

CREATE POLICY tenant_isolation ON public.alert_events USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

CREATE POLICY tenant_isolation ON public.alert_links USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

CREATE POLICY tenant_isolation ON public.alerts USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

CREATE POLICY tenant_isolation ON public.auth_group_mappings USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

CREATE POLICY tenant_isolation ON public.escalation_policies USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

CREATE POLICY tenant_isolation ON public.escalation_policy_steps USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

CREATE POLICY tenant_isolation ON public.field_mapping_templates USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

CREATE POLICY tenant_isolation ON public.incident_alert_links USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

CREATE POLICY tenant_isolation ON public.incident_assignees USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

CREATE POLICY tenant_isolation ON public.incident_comments USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

CREATE POLICY tenant_isolation ON public.incident_events USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

CREATE POLICY tenant_isolation ON public.incident_role_assignments USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

CREATE POLICY tenant_isolation ON public.incident_sla_policies USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

CREATE POLICY tenant_isolation ON public.incident_status_history USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

CREATE POLICY tenant_isolation ON public.incidents USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

CREATE POLICY tenant_isolation ON public.llm_providers USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

CREATE POLICY tenant_isolation ON public.mcp_servers USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

CREATE POLICY tenant_isolation ON public.on_call_overrides USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

CREATE POLICY tenant_isolation ON public.on_call_rotation_participants USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

CREATE POLICY tenant_isolation ON public.on_call_schedules USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

CREATE POLICY tenant_isolation ON public.on_call_working_hours USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

CREATE POLICY tenant_isolation ON public.password_reset_tokens USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

CREATE POLICY tenant_isolation ON public.playbook_phase_steps USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

CREATE POLICY tenant_isolation ON public.playbooks USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

CREATE POLICY tenant_isolation ON public.refresh_tokens USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

CREATE POLICY tenant_isolation ON public.roles USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

CREATE POLICY tenant_isolation ON public.tags USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

CREATE POLICY tenant_isolation ON public.tenant_ldap_config USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

CREATE POLICY tenant_isolation ON public.tenant_saml_config USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

CREATE POLICY tenant_isolation ON public.tenant_smtp_config USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

CREATE POLICY tenant_isolation ON public.tenant_storage_config USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

CREATE POLICY tenant_isolation ON public.upload_keys USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

CREATE POLICY tenant_isolation ON public.user_api_tokens USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

CREATE POLICY tenant_isolation ON public.users USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

CREATE POLICY tenant_isolation ON public.webhook_endpoints USING ((tenant_id = public.current_tenant_id())) WITH CHECK ((tenant_id = public.current_tenant_id()));

ALTER TABLE public.tenant_ldap_config ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.tenant_saml_config ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.tenant_smtp_config ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.tenant_storage_config ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.upload_keys ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.user_api_tokens ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.users ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.webhook_endpoints ENABLE ROW LEVEL SECURITY;

CREATE POLICY webhook_token_lookup ON public.webhook_endpoints FOR SELECT USING ((current_setting('app.webhook_lookup'::text, true) = 'true'::text));

