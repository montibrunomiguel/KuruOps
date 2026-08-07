// Mirrors the JSON shape emitted by backend/internal/domain (see the
// camelCase json tags added alongside this frontend so request/response
// casing is consistent across the API).

import type { Severity } from "./alerts";

export type UserRole = "admin" | "analyst" | "viewer";
// A capability set, not a mutually-exclusive choice -- mirrors
// domain.ResourceAccess on the backend (see db/migrations/0015_resource_access_capabilities.up.sql).
// "followup" grants the Dashboard's Follow-up view independently of the
// general Alerts/Incidents sections, which is what lets a SOC analyst be
// scoped to just Follow-up while a CSIRT member holds all three.
export type ResourceCapability = "alerts" | "incidents" | "followup";
export const RESOURCE_CAPABILITIES: ResourceCapability[] = ["alerts", "incidents", "followup"];
export type AuthProviderKind = "local" | "ldap" | "saml";

export interface User {
  id: string;
  tenantId: string;
  email: string;
  name: string;
  authProvider: AuthProviderKind;
  externalId?: string;
  role: UserRole;
  resourceAccess: ResourceCapability[];
  allowedTags: string[];
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
  role: UserRole;
  resourceAccess: ResourceCapability[];
  allowedTags: string[];
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
  createdBy?: string;
  createdAt: string;
  updatedAt: string;
}

export type MCPTransport = "stdio" | "http" | "sse";

export interface MCPServer {
  id: string;
  tenantId: string;
  name: string;
  transport: MCPTransport;
  endpointOrCommand: string;
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

// EscalationPolicy mirrors backend domain.EscalationPolicy -- Settings ->
// On-Call Escalation's per-severity rule for notifying whoever's on shift
// when an alert has stayed 'open' too long. No row for a severity means
// unconfigured, not "never escalate" as an explicit setting.
export interface EscalationPolicy {
  id: string;
  tenantId: string;
  severity: Severity;
  unacknowledgedAfterMinutes: number;
  channelType: EscalationChannelType;
  createdAt: string;
  updatedAt: string;
}

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
  provider: "s3" | "gcs";
  s3Bucket?: string;
  s3Region?: string;
  s3AccessKeyId?: string;
  gcsBucket?: string;
  gcsProjectId?: string;
  createdAt: string;
  updatedAt: string;
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
}

export interface LoginResponse {
  token: string;
  refreshToken: string;
  user: {
    id: string;
    email: string;
    name: string;
    role: UserRole;
    mustChangePassword: boolean;
  };
}
