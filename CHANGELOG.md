<p align="right"><a href="CHANGELOG.pt-BR.md">🇧🇷 Português</a> · <b>🇺🇸 English</b></p>

# Changelog

Format based on [Keep a Changelog](https://keepachangelog.com/). Dates in YYYY-MM-DD.

Since this project doesn't version releases yet (`v0.x`), the sections below group by development
milestone instead of tag — from now on, every relevant change (a feature, a security fix, a
user-visible behavior change) should get an entry here in the same PR that introduces it, not as
after-the-fact archaeology. See the matching item in `.github/PULL_REQUEST_TEMPLATE.md`'s checklist.

## [Unreleased]

### Changed

- **Project renamed from ArgusOps to KuruOps** — inspired by the Curupira, a figure from Brazilian
  folklore who protects the forest and warns its animals with a distinctive cry. The rename covers
  the entire codebase: the Go module path (`github.com/argusops/argusops` →
  `github.com/kuruops/kuruops`), every Docker image/container/volume/network name, the Postgres
  database and role names (`argusops`/`argusops_app`/`argusops_worker` →
  `kuruops`/`kuruops_app`/`kuruops_worker`), the `ARGUSOPS_APP_PASSWORD`/`ARGUSOPS_WORKER_PASSWORD`
  env vars (now `KURUOPS_*`), the default admin's seeded email
  (`admin@argusops.local` → `admin@kuruops.local`), Prometheus metric name prefixes, Kubernetes/
  Terraform resource names, frontend branding (page title, sidebar, login page, logo), and every
  doc. No functional change — purely a rename, verified with the full backend/frontend test suites,
  a fresh `docker compose up`, and a live login → webhook-create → alert-ingest → dashboard
  round-trip. Existing local deployments get fresh Docker volumes/database under the new project
  name (the old `argusops_*` ones aren't migrated automatically) — see the deploy docs if you need
  to carry data over instead of starting fresh.

### Added

- Indicators of Compromise (IOCs) on incidents: an "IOCs" button on the incident detail page opens
  a popup listing every IOC recorded so far, with an inline form to add a new one — type (a list
  covering NIST SP 800-61r3's own IOC examples — IP address, domain name, URL, file hash, email
  address/subject — plus the STIX 2.1 observable types NIST SP 800-150 points to for structured
  exchange: registry key, mutex, process name, user-agent, CVE, certificate fingerprint, and an
  "other" catch-all), the indicator value, an optional description, and the date it was identified.
  Append-only (no edit/delete, same as Team Notes comments) — `GET`/`POST /api/v1/incidents/{id}/iocs`.
  Automatically pulled into both the Markdown postmortem and the PDF report under a new "Indicators
  of Compromise (IOCs)" section — nothing further is needed to have a newly-recorded IOC show up in
  either document.
- Exportable incident PDF report: a "Download Report (PDF)" button on the incident detail page
  (`GET /api/v1/incidents/{id}/report.pdf`) — title, severity/priority, current phase, the full
  phase-history timeline with durations, team roles, description, tags, linked alerts, and team
  notes. Available at any phase, unlike the Markdown postmortem (which only appears once an
  incident reaches Post-Incident) — this is a point-in-time factual export, not a closing
  narrative. Pure-Go PDF generation (`github.com/jung-kurt/gofpdf`), no headless-browser/Chromium
  dependency added. Non-ASCII text (accented pt-BR titles/descriptions/notes, tags, names) is
  translated through `pdf.UnicodeTranslatorFromDescriptor` before rendering — the built-in "Arial"
  font gofpdf uses requires cp1252, and raw UTF-8 bytes fed to it directly come out as mojibake.
- Two-factor authentication (TOTP): self-service, optional per-user, enrolled from Settings →
  Profile → Two-Factor Authentication (scan a QR code or enter the secret manually into an
  authenticator app, then confirm with a 6-digit code). A local login for an enrolled account stops
  short of a session after the password check and returns a short-lived pending token instead; the
  login form's second step exchanges that token plus a fresh code for the real session
  (`POST /auth/mfa/verify`). Disabling requires re-entering the current password. Uses
  `github.com/pquerna/otp` (backend) and `qrcode.react` (frontend) — no secret or QR image ever
  leaves the browser except through the user's own enrollment request.
- Bulk status-change on Alerts/Incidents lists: a checkbox column + "select all on this page" on
  both list pages, with a toolbar that appears once ≥1 row is selected to change every selected
  alert's status (`POST /api/v1/alerts/bulk/status`) or every selected incident's NIST phase
  (`POST /api/v1/incidents/bulk/phase`) in one action. Implemented as a loop over the existing
  single-ID `ChangeStatus`/`ChangePhase` (not a new multi-row UPDATE), so every existing invariant
  (tag-based visibility, the "closed is terminal" guard, one audit event per affected item) keeps
  working unmodified; a partial failure (e.g. a row no longer visible to the caller) reports
  per-ID results rather than failing the whole request. Deliberately scoped to status/phase only —
  bulk-close is not supported (direct transitions to "closed"/"post_incident" are rejected),
  closing an alert or incident still requires the existing per-item classification/Close flow.
- Full-text search on Alerts and Incidents: a `q` filter on both list pages matches against title,
  source, rule ID and asset for alerts, and title/description for incidents — backed by a Postgres
  generated `tsvector` column and GIN index per table (`db/migrations/0008_fulltext_search`), not a
  slower `ILIKE` scan. The search box is debounced (300ms) before it re-queries, same as every
  other filter on these list pages resetting to page 1 on change.
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
  removed — only KuruOps's own record of the alert/incident, since nothing in this codebase has
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
- A settings-panel search box above the Settings nav groups (`SettingsLayout.tsx`) — filters items
  by translated label as you type, collapsing a whole group once none of its items match, instead
  of scrolling an ~18-item nav to find one you already know the name of.
- Two shared hooks replacing hand-rolled boilerplate duplicated across 6+ Settings panels:
  `useSavedFlag` (the "show a success message after a save" boolean) and
  `useAdminSingletonConfig` (the "GET one config object or null, edit local form state, PUT/DELETE
  to save, reload after success" shape), adopted by Retention/SMTP/Storage/Slack/Identity-Providers'
  LDAP+SAML panels and Profile/Incident-SLA respectively.
- Backend test coverage for previously-untested code: `internal/service/access.go`'s `tagsVisible`/
  `latestAnalysisFields`/`orEmptySlice`, `internal/blobstore`'s `S3Store`/`GCSStore` (mocking each
  SDK's own client the way `gdrive_test.go` already does for Google Drive), and a handler-level
  test for `analysis_tool_calls.go`'s approve/reject endpoints' already-resolved-status guard
  (previously only covered at the service layer, not through the HTTP handler).
- A shared `Modal` component (`frontend/src/components/Modal.tsx`) replacing 7 independent copies
  of the same `modal-overlay`/`modal` markup (`CloseAlertModal`, `AnalysisChat`,
  `IncidentsListPage`'s create-incident form, `PlaybookViewModal`, `OnCallTimeline`'s override
  popover, both `WebhooksPanel` modals) — none of which had `role="dialog"`/`aria-modal`,
  Escape-to-close, or any focus management. The new component adds all of that plus a real focus
  trap, modeled on `CommandPalette.tsx`'s existing dialog handling.
- A second `ErrorBoundary` around Settings' panel routes (`SettingsLayout.tsx`) — one broken
  settings panel now shows a scoped "this panel failed to load" message instead of taking down the
  whole app (the rest of Settings' nav stays usable, since it's outside the new boundary).

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
- `SlackIntegrationPanel.tsx` and its config-panel siblings (Retention/SMTP/Storage/Identity-
  Providers' LDAP+SAML) derived "not configured yet" from bare truthiness of the fetched config
  object, but a failed GET collapses to the same falsy value as "genuinely unconfigured" — a load
  failure showed the empty-state "Connect"/"Configure" CTA (plus, for Slack specifically, a fully
  clickable OAuth Connect button) as if nothing were set up, with only an easy-to-miss error banner
  alongside it as the actual signal something was wrong. Fixed by `useAdminSingletonConfig`'s new
  `configured` field, which is only true once loading and error have both resolved cleanly; Slack's
  Connect button is now replaced entirely by a "couldn't load status" message on a load failure,
  since clicking it in that state could start a fresh OAuth flow on top of unknown existing state.
- Deleted `frontend/src/components/PersonFilter.tsx` (zero imports, confirmed dead).
- **Security**: an escalation policy's webhook `Destination`, an MCP server's `endpoint`, and a
  self-hosted/OpenAI-compatible LLM provider's `base_url` were all dialed with a plain
  `http.Client` — anyone with Settings access to those three areas could point one at
  `http://169.254.169.254/...` (a cloud metadata endpoint) or an internal-only service and get
  KuruOps to make that request for them (SSRF). All three now dial through a new
  `internal/httpguard.NewClient`, which refuses to connect to a loopback/link-local/private
  address (checked against the resolved IP, not just the hostname string, so it isn't bypassable
  by DNS rebinding); a genuinely on-prem deployment can opt out with
  `ALLOW_PRIVATE_NETWORK_TARGETS=true`.
- **Security**: `secrets.NewFromConfig` now refuses to start with the exact `SECRETS_ENCRYPTION_KEY`
  value committed in `.env.example` unless `AUTH_MODE` is `dev`/`dev-headers` — that key is real
  and working (for local-dev convenience), which made it a known shared key if ever copy-pasted
  into a real deployment instead of generated fresh.
- **Security**: the Google Drive and Slack OAuth callback handlers put the raw Go error text
  (which can carry internal detail — a DB error, a state-lookup failure) directly into the
  redirect URL's query string on failure. The real error is now logged server-side only; the
  redirect gets a fixed generic code (`?gdrive_error=connection_failed`) instead.
- Password policy (`AuthService.ChangePassword`, `PasswordResetService.ConfirmReset`) was
  length-only, accepting `"11111111"`/`"aaaaaaaa"` — now also requires at least one letter and one
  digit (deliberately not a symbol/complexity rule, which mostly just pushes people toward
  predictable substitutions).
- `LinkedAlertsPanel`'s unlink button and `ScheduleForm`'s concurrent-analysts +/- steppers had
  hardcoded, untranslated `aria-label`s (`"unlink"`, `"-"`, `"+"`) instead of going through i18n
  like every other label in this app.
- **Security**: an on-call escalation policy's Slack channel `Destination` (an admin-pasted
  Slack "Incoming Webhook" URL) was dialed with the plain `http.Client`, not the SSRF-guarded
  `internal/httpguard` client the equivalent generic-webhook channel already used for the same
  kind of admin-supplied destination — Slack's destination was never actually validated to be a
  real `hooks.slack.com` URL, so it was just as spoofable to an internal/loopback/link-local
  target. Now dials through the same guarded client.
- **Security**: a SAML identity provider's metadata URL (Settings → Identity Providers) was
  fetched with `http.DefaultClient` instead of the SSRF-guarded client — same admin-configured-URL
  category the webhook/MCP/LLM guard already covers, just missed when that guard was added.
- **Security**: outbound SMTP STARTTLS (`mailer.SMTPSender`) built its `tls.Config` with no
  `MinVersion`, allowing negotiation down to TLS 1.0 against a permissive or misconfigured mail
  relay. Pinned to TLS 1.2.

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
