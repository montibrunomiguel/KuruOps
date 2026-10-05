// Mirrors the JSON shape emitted by backend/internal/domain (see the
// camelCase json tags added alongside this frontend so request/response
// casing is consistent across the API).

import type { Severity } from "./alerts";

// A capability set, not a mutually-exclusive choice -- mirrors
// domain.ResourceAccess on the backend (see db/migrations/0015_resource_access_capabilities.up.sql).
// "followup" grants the Dashboard's Follow-up view independently of the
// general Alerts/Incidents sections, which is what lets a SOC analyst be
// scoped to just Follow-up while a CSIRT member holds all three.
export type ResourceCapability = "alerts" | "incidents" | "followup";
export const RESOURCE_CAPABILITIES: ResourceCapability[] = ["alerts", "incidents", "followup"];
export type AuthProviderKind = "local" | "ldap" | "saml";

// Role mirrors backend domain.Role -- Settings -> Roles' reusable access
// bundle (admin gate + resource capabilities + tag scope), assigned to
// users and to LDAP/SAML group mappings. Replaces the old fixed
// admin/analyst/viewer tier: nothing in the backend ever branched on that
// tier beyond "is this an admin or not", so IsAdmin is the only tier
// distinction that survived -- see domain.Role's doc comment.
export interface Role {
  id: string;
  tenantId: string;
  name: string;
  isAdmin: boolean;
  resourceAccess: ResourceCapability[];
  allowedTags: string[];
  createdAt: string;
  updatedAt: string;
}

export interface User {
  id: string;
  tenantId: string;
  email: string;
  name: string;
  authProvider: AuthProviderKind;
  externalId?: string;
  phone?: string;
  roleId: string;
  role: Role;
  isActive: boolean;
  lastLoginAt?: string;
  createdAt: string;
  updatedAt: string;
}

// Only ever present in the response to POST /api/v1/settings/users -- not
// retrievable again afterward (see backend's createUserResponse).
export interface CreatedUser {
  user: User;
  temporaryPassword: string;
}

export interface AuthGroupMapping {
  id: string;
  tenantId: string;
  provider: "ldap" | "saml";
  externalGroup: string;
  roleId: string;
  role: Role;
  createdAt: string;
}

export interface WebhookEndpoint {
  id: string;
  tenantId: string;
  name: string;
  source: string;
  tokenLast4: string;
  status: "active" | "disabled";
  rotatedAt?: string;
  // undefined = admin explicitly opted this endpoint out of expiring.
  expiresAt?: string;
  createdBy?: string;
  createdAt: string;
  // Unlike name/source, changeable after creation -- see WebhooksPanel's
  // "change template" action.
  fieldMappingTemplateId?: string;
  // Empty means dedup is off. Changeable after creation via the "group by
  // fields" editor -- same pattern as fieldMappingTemplateId above.
  groupByFields: string[];
  dedupWindowMinutes: number;
}

export interface FieldMappingRule {
  jsonPath: string;
  label: string;
}

export interface FieldMappingTemplate {
  id: string;
  tenantId: string;
  name: string;
  rules: FieldMappingRule[];
  createdBy?: string;
  createdAt: string;
  updatedAt: string;
}

export interface UserAPIToken {
  id: string;
  tenantId: string;
  userId: string;
  name: string;
  tokenLast4: string;
  // undefined = the user explicitly opted this token out of expiring.
  expiresAt?: string;
  createdAt: string;
  // undefined = still active.
  revokedAt?: string;
}

export type LLMProviderKind = "anthropic" | "openai_compatible" | "azure_openai" | "self_hosted";

export interface LLMProvider {
  id: string;
  tenantId: string;
  name: string;
  kind: LLMProviderKind;
  baseUrl?: string;
  model: string;
  isDefault: boolean;
  // When true and this is the tenant's default provider, every alert
  // ingested via webhook is analyzed automatically -- otherwise (the
  // default) analysis only happens when an analyst clicks "Analisar com IA".
  autoAnalyzeAllAlerts: boolean;
  createdBy?: string;
  createdAt: string;
  updatedAt: string;
}

export type MCPTransport = "stdio" | "http" | "sse";
export type MCPAuthType = "none" | "api_key" | "bearer" | "oauth";

export interface MCPServer {
  id: string;
  tenantId: string;
  name: string;
  transport: MCPTransport;
  endpointOrCommand: string;
  // Authentication. Only the non-secret parameters come back from the API --
  // every credential (API key, bearer token, OAuth client secret) is
  // write-only. A type that needs a secret implies one is stored.
  authType: MCPAuthType;
  authHeaderName?: string;
  oauthTokenUrl?: string;
  oauthClientId?: string;
  // When true the agent is offered every tool the server exposes and
  // allowedTools is ignored; a tool still needs analyst approval unless the
  // server declares it read-only (see DiscoveredTool.annotations).
  allowAllTools: boolean;
  allowedTools: string[];
  enabledFor: string[];
  sideEffectingTools: string[];
  isEnabled: boolean;
  createdBy?: string;
  createdAt: string;
  updatedAt: string;
}

export type ToolCallStatus = "proposed" | "approved" | "rejected" | "executed" | "failed";

// AIToolCall mirrors backend domain.AIToolCall -- a side-effecting MCP tool
// call the "Analyze with AI" agentic loop proposed and paused on, awaiting
// an analyst's approve/reject (see AIAnalysisService's agentic loop on the
// backend). Only ever shows up here at status="proposed" -- once approved/
// rejected it drops out of the pending-approvals list.
export interface AIToolCall {
  id: number;
  tenantId: string;
  mcpServerId: string;
  toolName: string;
  contextType: "alert" | "incident";
  contextId: string;
  args: Record<string, unknown>;
  result?: unknown;
  status: ToolCallStatus;
  approvedBy?: string;
  approvedAt?: string;
  createdAt: string;
}

export type EscalationChannelType = "pagerduty" | "slack" | "webhook";

// EscalationStep mirrors backend domain.EscalationStep -- one link in an
// Escala de Acionamento chain, referencing an OnCallSchedule (not
// necessarily the tenant's default) to resolve the on-call analyst against
// when it fires.
export interface EscalationStep {
  id: string;
  policyId: string;
  position: number;
  scheduleId: string;
  scheduleName: string;
  delayMinutes: number;
  channelType: EscalationChannelType;
  // Only meaningful when channelType is "webhook" -- undefined means the
  // default fixed payload shape (see backend notifier.WebhookSender).
  webhookPayloadTemplate?: string;
}

// EscalationPolicy mirrors backend domain.EscalationPolicy -- Settings ->
// Escala de Acionamento's per-severity ordered chain of steps, fired either
// by the automatic SLA loop (cmd/worker's sweepEscalations, wraps back to
// step 0 once the chain is exhausted) or by manually escalating an alert
// (advances exactly one step, independent counter, never wraps). No row for
// a severity means unconfigured, not "never escalate" as an explicit
// setting.
export interface EscalationPolicy {
  id: string;
  tenantId: string;
  severity: Severity;
  steps: EscalationStep[];
  createdAt: string;
  updatedAt: string;
}

// SaveEscalationStepRequest mirrors backend handlers' saveEscalationStepRequest
// -- Destination is plaintext; "" for a position that already had a step
// saved there means keep the existing secret (see EscalationPolicyService.Save).
export interface SaveEscalationStepRequest {
  scheduleId: string;
  delayMinutes: number;
  channelType: EscalationChannelType;
  destination: string;
  webhookPayloadTemplate: string;
}

// The placeholders a playbook step's webhook payload template can use --
// mirrors the alert-only subset of backend notifier.WebhookPlaceholders,
// kept here rather than fetched so the panel can show them without an extra
// round trip. Deliberately excludes the analyst placeholders below: a
// playbook step trigger has no on-call schedule to resolve an analyst
// against, so those would always render empty here.
export const WEBHOOK_PAYLOAD_PLACEHOLDERS = ["{{title}}", "{{description}}", "{{severity}}", "{{alertId}}", "{{url}}"] as const;

// The placeholders an Escala de Acionamento step's webhook payload template
// can use -- the alert placeholders above, plus the resolved on-call
// analyst's name/email/phone (empty if no one's currently on shift for the
// step's schedule). Mirrors backend notifier.WebhookPlaceholders exactly.
export const ESCALATION_WEBHOOK_PLACEHOLDERS = [
  ...WEBHOOK_PAYLOAD_PLACEHOLDERS,
  "{{analystName}}", "{{analystEmail}}", "{{analystPhone}}",
] as const;

// TargetDatabaseConfig is the request body for both
// /settings/database-migration/test-connection and .../migrate -- mirrors
// backend handlers.targetConfigRequest.
export interface TargetDatabaseConfig {
  host: string;
  port: number;
  database: string;
  user: string;
  password: string;
  sslMode: string;
}

export interface TableRowCount {
  source: number;
  target: number;
}

// MigrationResult mirrors backend dbmigrate.MigrationResult -- what
// Settings -> External Database shows once a migration succeeds. appDsn/
// workerDsn carry the freshly generated role passwords in plain text (the
// only place they're ever shown) -- the admin must copy them into
// DATABASE_URL before restarting the stack, see DatabaseMigrationPanel.
export interface MigrationResult {
  schemaVersion: number;
  rowCounts: Record<string, TableRowCount>;
  appDsn: string;
  workerDsn: string;
}

export interface LDAPConfig {
  tenantId: string;
  host: string;
  port: number;
  useTls: boolean;
  bindDn: string;
  userBaseDn: string;
  userFilter: string;
  groupBaseDn: string;
  groupAttribute: string;
  createdAt: string;
  updatedAt: string;
}

export interface SAMLConfig {
  tenantId: string;
  idpMetadataUrl?: string;
  idpMetadataXml?: string;
  spEntityId: string;
  acsUrl: string;
  groupAttribute?: string;
  createdAt: string;
  updatedAt: string;
}

// StorageConfig mirrors backend/internal/domain.StorageConfig -- the
// secret-bearing fields (access key, credentials JSON) never appear here,
// same reasoning as LDAPConfig/SAMLConfig above.
export interface StorageConfig {
  tenantId: string;
  provider: "s3" | "gcs" | "gdrive";
  s3Bucket?: string;
  s3Region?: string;
  s3AccessKeyId?: string;
  gcsBucket?: string;
  gcsProjectId?: string;
  gdriveFolderId?: string;
  gdriveAuthMethod?: "service_account" | "oauth";
  gdriveOauthConnectedEmail?: string;
  createdAt: string;
  updatedAt: string;
}

// SlackConfig mirrors backend/internal/domain.SlackConfig -- the bot token
// is never sent back, same reasoning as StorageConfig above. A non-null
// SlackConfig is the "is Slack configured" gate future Slack-dependent
// features (thread sync, incident channel linking, ...) will check before
// offering their UI -- see the Slack integration foundation plan.
export interface SlackConfig {
  tenantId: string;
  teamId: string;
  teamName: string;
  botUserId: string;
  installedByUserId: string;
  installedByUserName: string;
  grantedScopes: string;
  createdAt: string;
  updatedAt: string;
}

// RetentionConfig mirrors backend/internal/domain.RetentionConfig -- unlike
// StorageConfig/SMTPConfig/SlackConfig, GET never returns null: retention
// is on by default (18 months for each resource type), so `configured`
// distinguishes an admin's actual saved value from the synthesized
// default.
export interface RetentionConfig {
  tenantId: string;
  alertRetentionMonths: number;
  incidentRetentionMonths: number;
  configured: boolean;
  updatedAt?: string;
}

// AdminAuditLogEntry mirrors backend/internal/service.AdminAuditLogEntry --
// one row of Settings -> Data & Audit's change log. `data` is the raw
// {"from": ..., "to": ...} diff every feeding service writes (see
// domain.AdminAuditEvent's doc comment); shape varies by `area`/`action`,
// so it's left untyped here and rendered as formatted JSON rather than
// modeled field-by-field for every one of the ~15 areas.
export interface AdminAuditLogEntry {
  id: number;
  tenantId: string;
  area: string;
  action: string;
  actorType: "user" | "system" | "ai";
  actorId: string;
  actorName: string;
  data: Record<string, unknown>;
  createdAt: string;
}

export interface AdminAuditLogCursor {
  createdAt: string;
  id: number;
}

export interface AdminAuditLogPage {
  events: AdminAuditLogEntry[];
  nextCursor: AdminAuditLogCursor | null;
}

// SMTPConfig mirrors backend/internal/domain.SMTPConfig -- the password is
// never sent back, same reasoning as StorageConfig above.
export interface SMTPConfig {
  tenantId: string;
  host: string;
  port: number;
  useTls: boolean;
  username: string;
  fromAddress: string;
  fromName?: string;
  createdAt: string;
  updatedAt: string;
}

// DiscoveredTool mirrors backend/internal/mcpclient.Tool — the raw catalog
// from a live tools/list call, before an admin picks which ones become
// MCPServer.allowedTools / sideEffectingTools.
export interface DiscoveredTool {
  name: string;
  description: string;
  // MCP tool behavior hints, as declared by the server. Only readOnlyHint
  // lets a tool run without approval, and only on an allow-all server.
  annotations?: { readOnlyHint?: boolean; destructiveHint?: boolean };
}

// AnalysisChatMessage/AnalysisChatTranscript mirror backend
// service.ChatMessage/ChatTranscript -- the "Analisar com IA" chat's
// transcript (see components/AnalysisChat.tsx). role is "user" | "assistant"
// | "tool"; toolCalls is only ever set on an "assistant" message that
// requested tool use, toolCallId only on the "tool" message answering it.
export interface AnalysisChatToolCall {
  id: string;
  name: string;
  args: Record<string, unknown>;
}

export interface AnalysisChatMessage {
  role: "user" | "assistant" | "tool";
  content: string;
  toolCalls?: AnalysisChatToolCall[];
  toolCallId?: string;
}

// status/runId/pendingToolCallId/error are omitted by the backend
// (omitempty) when no analysis run exists yet -- an empty chat, not an
// error.
export interface AnalysisChatTranscript {
  runId?: number;
  status?: "running" | "paused" | "completed" | "failed";
  messages: AnalysisChatMessage[];
  pendingToolCallId?: number;
  error?: string;
}

// The response shape from POST /api/v1/alerts/bulk/status and
// /api/v1/incidents/bulk/phase -- one entry per requested id, since a
// partial failure (e.g. one of N selected rows is no longer visible to the
// caller) doesn't fail the whole request. See backend's service.BulkResult.
export interface BulkResult {
  id: string;
  success: boolean;
  error?: string;
}

export interface BulkResponse {
  results: BulkResult[];
}

// What POST /auth/refresh answers. It carries the identity as well as the
// token because the access token is never stored: a page load has only the
// HttpOnly cookie to go on, and a SAML callback has not even got a stored
// user yet.
export interface RefreshResponse {
  token: string;
  user: LoginResponse["user"];
}

export interface LoginResponse {
  token: string;
  // No refreshToken field on purpose: the backend delivers it as an
  // HttpOnly cookie (see internal/sessioncookie), which is only worth
  // anything if the value never also reaches somewhere script can read it.
  user: {
    id: string;
    email: string;
    name: string;
    phone?: string;
    // The assigned Role's display name (see backend loginUser.Role) --
    // authorization itself is decided from the JWT's is_admin/
    // resource_access claims (see AuthContext.decodeTokenClaims), never
    // from this string.
    role: string;
    mustChangePassword: boolean;
  };
}

// What POST /auth/login returns instead of LoginResponse when the account
// has TOTP enrolled: the password was correct, but no session exists yet --
// pendingToken must be presented alongside a 6-digit code to
// POST /auth/mfa/verify (which itself returns a normal LoginResponse) before
// there's a real session. See AuthContext.loginLocal/verifyMfa.
export interface MfaRequiredResponse {
  mfaRequired: true;
  pendingToken: string;
}
