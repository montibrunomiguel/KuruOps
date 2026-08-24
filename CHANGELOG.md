<p align="right"><a href="CHANGELOG.pt-BR.md">🇧🇷 Português</a> · <b>🇺🇸 English</b></p>

# Changelog

Format based on [Keep a Changelog](https://keepachangelog.com/). Dates in YYYY-MM-DD.

Since this project doesn't version releases yet (`v0.x`), the sections below group by development
milestone instead of tag — from now on, every relevant change (a feature, a security fix, a
user-visible behavior change) should get an entry here in the same PR that introduces it, not as
after-the-fact archaeology. See the matching item in `.github/PULL_REQUEST_TEMPLATE.md`'s checklist.

## [Unreleased]

### Added

- Settings → Data & Audit → Audit Log: an append-only record of every Settings change any admin
  has made — area, action, actor, and a before/after diff (`admin_audit_events` table,
  `AdminAuditEventRepository.InsertEvent`). Covers all ~15 Settings areas (Webhooks, Field Mapping
  Templates, AI Integration, MCP Servers, Users, Roles, Identity Providers, Tags, Storage
  Integration, SMTP, Slack, On-Call Schedule, Incident SLAs, Escalation Chain, Retention) — every
  mutating method in each of those services now writes one event in the same transaction as the
  state change it records, so a failed audit write rolls back the change too. Distinct from the
  existing Audit Export (`AuditExportService`), which covers alert/incident history for SIEM
  export, not Settings changes. Read via `GET /api/v1/settings/audit-log`, keyset-paginated newest
  first.
- Settings → Data & Audit → Retention: configurable data retention for closed alerts/incidents
  (default 18 months, separately configurable per resource type). A closed alert/incident is
  permanently deleted by `backend/cmd/worker`'s hourly `sweepDataRetention` job once its retention
  period elapses since it closed — an open one is never touched, no matter its age. Evidence
  attached to a deleted alert/incident (images in S3/GCS/Google Drive/local disk) is never
  removed — only ArgusOps's own record of the alert/incident, since nothing in this codebase has
  ever implemented deleting from blob storage in the first place.
- Settings → Conectores → Slack: connect/disconnect a Slack workspace via bot-token OAuth
  (`internal/slackclient`, `SlackConfigService`). Foundation only — no message/thread/channel sync
  yet; a non-null `SlackConfigService.Get` is the gate future Slack features will check before
  offering their UI. See `docs/SLACK_APP_SETUP.md` for the Slack App Manifest needed to configure
  `SLACK_CLIENT_ID`/`SLACK_CLIENT_SECRET`. Introduces a shared `oauth_states` table/
  `OAuthStateService` (single-use, DB-backed CSRF token) reused by the Google Drive OAuth flow
  below.
- Google Drive as a third Storage Integration provider (alongside S3/GCS), with a choice of a
  pasted service-account key or a full "Connect your Google account" OAuth flow
  (`blobstore.GDriveStore`, `StorageConfigService.SaveGDriveServiceAccount`/
  `HandleGDriveOAuthCallback`).
- JSON export (`GET /api/v1/settings/audit-export/json`, newline-delimited) alongside the existing
  CEF export, sharing the same keyset-pagination cursor logic.
- Architecture diagram (mermaid) in the root README, `CHANGELOG.md`, `docs/TROUBLESHOOTING.md`,
  and an OpenAPI spec for `/api/v1/**` (documentation that used to be prose-only, or missing).
- `golangci-lint` (backend) and ESLint (frontend) set up from scratch, with their own gate in
  `task test`; `govulncheck`/`npm audit` also gated in `task test`; a coverage-regression gate
  script (`backend/scripts/check-coverage-baseline.sh`).
- A reusable fake LDAP server (`internal/testutil/fake_ldap.go`) and a SAML metadata helper
  (`internal/testutil/fake_saml.go`) for testing `AuthenticateLDAP`/`SAMLAuthService` without
  depending on a real external directory/IdP.
- Real integration tests (gated behind an env var, don't run in the default `task test`) against
  real infrastructure: `task backend:test:vault` (HashiCorp Vault dev-mode) and
  `task backend:test:mcp` (the official `@modelcontextprotocol/server-everything` reference
  server).
- `AUTH_MODE=dev` now persists the generated JWT keypair in `.dev-keys/` (a named Docker volume)
  instead of generating a fresh one on every `api` container restart — sessions survive a normal
  `docker compose restart`/redeploy.

### Fixed

- **Security**: `middleware.NewRateLimiter` (used for login rate limiting) trusted
  `X-Forwarded-For`, a header the client itself controls — an attacker could bypass the login
  attempt limit just by varying that header on every request. It now trusts only `X-Real-IP`,
  which `nginx.conf` unconditionally overwrites on every proxied request.
- **Security**: the `goxmldsig` dependency (SAML flow's XML signing/validation) updated to fix a
  signature-bypass vulnerability caused by loop-variable capture (GO-2026-4753). `chi`, `pgx`, and
  the Go toolchain also updated to close the rest of `govulncheck`'s findings.
- **Security**: `MCPToolService.RejectToolCall` didn't check whether a tool call had already been
  resolved before overwriting its status — it allowed reverting an already-approved/executed tool
  call back to "rejected", corrupting the audit trail. It now uses the same existence/status guard
  `ApproveToolCall` already had.
- The SAML flow's correlation cookie (`SameSite=Lax`) was never sent on the cross-site POST every
  real IdP uses to return the assertion — the `AuthnRequest`'s ID now travels via `RelayState`
  (echoed by any spec-compliant IdP) as the primary channel, with the cookie as a same-site
  fallback.
- `secrets.EnvStore` lost every credential on every process restart while rows in the database kept
  referencing the old ref — replaced with `PersistentEnvStore` (AES-256-GCM, persisted in the
  `secret_store` table) as the default backend.
- `CommandPalette.tsx` used i18n keys that didn't exist (`nav.dashboard` instead of
  `sidebar.nav.dashboard`), so it always showed the raw key instead of the translated text; the
  "profile" item also linked to an admin-only page instead of `/profile`.
- `WebhooksPanel.tsx`: regenerating a webhook token revealed the plaintext value and then, right
  after, fired a reload that unmounted the row before the user could actually see the token — the
  whole point of revealing it was silently defeated.

## [2026-08-07] — `78f41f5`

### Added

- A real secret store backend (`PersistentEnvStore` as the default; `VaultStore`/`AWSKMSStore` as
  alternatives via `SECRETS_BACKEND`).
- Dashboard metrics: alert/incident MTTA/MTTR, daily alert/incident volume (materialized views
  recomputed by the `worker`), filters by severity/status/priority/phase/analyst/commander/tag and
  time range (preset or custom range).
- NIST 800-61 team roles on the incident (Commander, Technical Lead, Incident Handler(s),
  Communications Lead, Privacy Officer) and phase history with an auditable correction flow.
- A real AI agentic loop (`AIAnalysisService.runAgentAnalysis`): analysis can propose tool calls
  via MCP, pause on side-effecting tools until an analyst approves, and resume where it left off.
- Dedicated ingest normalizers for Wazuh, CrowdStrike, and GuardDuty.
- Real-time notifications via SSE (`internal/events`, `EventsHandlers.Stream`).
- On-call escalation (PagerDuty/Slack/generic webhook) and CEF audit export.
- Extensive backend and frontend test coverage.

## [2026-08-06] — `e235d44`

- Initial implementation: alerts, incidents, playbooks, local authentication, webhook ingest,
  basic Settings.
