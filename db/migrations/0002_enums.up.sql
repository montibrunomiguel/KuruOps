create type severity_enum as enum ('critical', 'high', 'medium', 'low', 'informational');

create type alert_status_enum as enum ('open', 'investigating', 'escalated', 'closed');

create type classification_enum as enum ('false_positive', 'true_positive', 'authorized_event');

create type incident_phase_enum as enum (
  'new', 'detection_analysis', 'containment', 'eradication', 'recovery', 'post_incident'
);

create type incident_priority_enum as enum ('p1', 'p2', 'p3', 'p4');

create type user_role_enum as enum ('admin', 'analyst', 'viewer');

create type resource_access_enum as enum ('both', 'alerts', 'incidents');

create type auth_provider_enum as enum ('local', 'ldap', 'saml');

create type actor_type_enum as enum ('user', 'system', 'ai');

create type mcp_transport_enum as enum ('stdio', 'http', 'sse');

create type llm_kind_enum as enum ('anthropic', 'openai_compatible', 'azure_openai', 'self_hosted');

create type tool_call_status_enum as enum ('proposed', 'approved', 'rejected', 'executed', 'failed');
