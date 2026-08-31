<p align="right"><a href="CHANGELOG.pt-BR.md">🇧🇷 Português</a> · <b>🇺🇸 English</b></p>

# Changelog

Format based on [Keep a Changelog](https://keepachangelog.com/). Dates in YYYY-MM-DD.

Since this project doesn't version releases yet (`v0.x`), the sections below group by development
milestone instead of tag — from now on, every relevant change (a feature, a security fix, a
user-visible behavior change) should get an entry here in the same PR that introduces it, not as
after-the-fact archaeology. See the matching item in `.github/PULL_REQUEST_TEMPLATE.md`'s checklist.

## [Unreleased]

### Changed

- Playbooks no longer offer a "New" phase section under "Steps by Phase" — by the time an
  incident is still in New/Identification, an analyst hasn't triaged it yet, so there was never
  anything for a response playbook to prescribe there (steps only make sense from Detection &
  Analysis onward). Applies to both the editor (`PlaybookDetailPage.tsx`) and the read-only
  trigger popup (`PlaybookViewModal.tsx`); the backend itself is untouched (still generically
  accepts any NIST phase for a playbook step) — this is a UI-only scoping of what gets offered/
  displayed, not a data-model change. No existing playbook had any "New"-phase steps to begin
  with, so nothing was migrated or dropped.
- `AlertsListPage.tsx`/`IncidentsListPage.tsx` had independently grown the exact same ~25-line
  row-selection state machine (a `Set` of selected ids, select-all with the header checkbox's
  indeterminate state, reset on filter/page change) and the same bulk-action apply/error/summary
  state. Extracted into shared `useRowSelection`/`useBulkAction` hooks — no behavior change, both
  pages' existing tests pass unmodified.
- `useSidebarCounts.ts` (the sidebar's "Alerts"/"Incidents" badge counts) was the one remaining
  hand-rolled `useEffect`+`fetch`+cancelled-flag data-fetching hook in the app, with no live-update
  subscription — the counters only changed on a full page navigation, unlike every list page's own
  counts. Rebuilt on `useList` (react-query) + `useEventStream`, the same pattern every other
  list/detail page already uses; the counters now update live over SSE the instant another
  analyst's change arrives, same as the list pages. `useList` gained an `options.enabled` flag
  (default `true`, every existing call site unaffected) to support this -- skips the fetch entirely
  for a capability the caller doesn't have, instead of firing a request guaranteed to 403.

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
- Settings navigation groups (Integrations, Connectors, Identity & Access, Operations, Data &
  Audit) now collapse and expand — click a group's label to toggle it, instead of always scrolling
  past every category's full item list. The group containing whatever settings page you're
  currently on always stays expanded, and searching the nav still surfaces a match from an
  otherwise-collapsed group. Per-group collapsed state persists across reloads (`localStorage`).

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

- **Security**: tag-based visibility (`Role.allowedTags`) was enforced only on the "main" alert and
  incident routes — every sub-resource route queried by ID under tenant RLS alone. Nothing forces
  an HTTP client to call `GET /incidents/{id}` before `GET /incidents/{id}/comments`, so an
  analyst restricted to one set of tags could read the Team Notes, IOCs, timeline, status history
  and linked alerts of an alert/incident they cannot see, post new comments and IOCs onto it, and
  approve its paused MCP tool calls — the last of which *executes* a real side-effecting
  automation. All 42 `{id}`-scoped alert/incident routes now re-check visibility, answering 404
  exactly like `Get` does (so "no such incident" and "exists but hidden from you" stay
  indistinguishable). A new `TestSubResourceRoutesRejectTagRestrictedCaller` walks the registered
  chi routes rather than a hand-written list, so a future sub-resource that forgets its gate fails
  CI the moment it's registered.
- **Security**: a failed `RevokeSessions` during user deactivation was discarded silently. Not
  rolling back the deactivation is still the right call, but with a 30-day refresh-token TTL a
  swallowed failure could leave a deactivated (possibly compromised) account with a renewable
  session for a month while the admin saw a clean 204. Now logged.
- The Slack and Google OAuth callback flows made outbound calls with no timeout at all
  (`http.DefaultClient` / oauth2's default), on a route mounted outside the API group's request
  timeout — so a blackholed connection to either provider pinned a goroutine and its connection
  indefinitely. Both now use a 15s-bounded client.
- Five reorderable/removable list forms keyed their rows by array index
  (`EscalationEditForm.tsx`'s escalation steps, `ScheduleForm.tsx`'s working-hours intervals,
  `PlaybookDetailPage.tsx`'s playbook steps, `FieldMappingTemplatesPanel.tsx`'s mapping rules,
  `GroupByFieldsEditor.tsx`'s dedup fields) — removing or reordering a row shifts every later
  row's index, so React reuses each shifted row's DOM node (and whatever focus/state it was
  holding) for a *different* logical row than the one it was actually rendering a moment before.
  Every list now keys by a stable id instead: a `crypto.randomUUID()` generated once per row
  (stripped again before the save payload, alongside the read-only playbook-step list, which had
  a real backend id available all along and now uses it) for the four object-array cases, and a
  small parallel id array for `GroupByFieldsEditor`'s plain `string[]` prop, which has no object to
  hang an id off.
- `LinkSearchPicker.tsx` (the alert-to-alert / incident-to-alert correlation search, used by
  AlertDetailPage's Linked Alerts panel and IncidentDetailPage's correlated-alerts panel) rendered
  its results as plain `<div onClick>` rows — unreachable by keyboard at all, no way to Tab into a
  result or link one without a mouse. Rebuilt on the same `role="combobox"`/
  `aria-activedescendant`/arrow-key pattern already proven out in `CommandPalette.tsx`: Arrow
  Up/Down moves the active result (wrapping), Enter links it, Escape clears the query.
- `OnCallTimeline.tsx`'s colored on-call bars hardcoded white text — fails WCAG AA's 4.5:1
  small-text contrast minimum against 9 of the palette's 10 colors (as low as 1.59:1 on the
  yellow). `lib/personColor.ts` gained `personTextColor`, which picks black or white per swatch
  (whichever actually clears 4.5:1 against that specific background) instead of a color assumed
  safe for all of them.
- Two destructive actions skipped the app's own inline confirm/cancel pattern (`useConfirm`,
  chosen elsewhere specifically because `window.confirm()` silently auto-dismisses in some
  embedded browser contexts) and deleted immediately on a single click: `LinkedAlertsPanel.tsx`'s
  unlink button and `OnCallTimeline.tsx`'s on-call override removal. Both now ask for confirmation
  first, matching every other destructive action in Settings.
- External-database migration (Settings → Data & Audit, `internal/dbmigrate`) would fail copying
  any table with a full-text-search `generated always as (...) stored` column
  (`alerts.search_vector`, `incidents.search_vector`) with `"row field count is N, expected N-1"`
  — `COPY ... TO` includes a generated column's computed value by default, `COPY ... FROM` does
  not accept one, so a bare `COPY tablename` with no explicit column list sent one more field per
  row than the target side was prepared to read. Fixed by resolving each table's non-generated
  columns once and using that exact column list explicitly on both the read and write side. Caught
  by wiring `internal/dbmigrate`'s own integration test suite into CI for the first time (see
  below) — it had never run automatically before, so this had zero test coverage in practice.
- No detached background goroutine (AI analysis runs, manual-escalation notification sends, the
  external-DB-migration row-copy stream, every long-running background loop) had panic recovery —
  `chi.Recoverer` only protects the synchronous HTTP request path, so an unhandled panic on any of
  them took the entire process down. New `internal/safego.Go` wraps every one of them, recovering
  and logging a panic instead of crashing.
- Three call sites silently discarded a real error (`AIAnalysisService.finishSimpleRun`/`failRun`,
  `MCPToolService.recordFailure`) — a failed write left an AI analysis run stuck showing "running"
  forever, or a tool call's database status permanently out of sync with its actual outcome, with
  no log trace anywhere of why. Now logged via `slog.Error`.
- A burst of alerts arriving faster than an LLM call completes could spawn an unbounded number of
  concurrent auto-analysis goroutines, one per alert — now capped at 5 concurrent in-flight
  analyses via a counting semaphore.
- 62 handler call sites across 24 files wrote a raw Go error string (`err.Error()`) straight into
  a 500 response body — a Postgres constraint/column name, a driver-level error, or an internal
  file path routinely leaking into an API response. A new `writeInternalError` helper now logs the
  real error server-side and returns a generic message to the client instead.
- `docs/openapi.yaml` documented `/auth/login`'s 401/429 responses as `text/plain` — they're
  actually `application/json` (`{"error": "..."}`), same as every other handler-level error in
  this API; only the shared, genuinely-plain-text `Unauthorized`/`Forbidden` responses (issued by
  the auth middleware, not a handler) are meant to differ.
- Removed `RoleService.Get` — no HTTP route or other service ever called it, only its own test
  file, as a convenience assertion helper now replaced with a direct repository read.
- **Security**: `AuthenticateLDAP` re-bound as the resolved user DN with whatever password the
  caller sent, including an empty one — RFC 4513 §5.1.2 makes a bind with a valid DN and a
  zero-length password an "unauthenticated bind" that many directories (including default
  OpenLDAP) treat as successful without checking anything, so any LDAP-provisioned user could be
  logged into just by knowing their email. An empty/whitespace-only password is now rejected
  before ever dialing the directory.
- **Security**: refresh tokens were never revoked on logout (there was no logout endpoint at all)
  or on password change/reset — a stolen refresh token kept working for its full 30-day TTL even
  after the legitimate user changed their password. Added `POST /auth/logout` (revokes exactly the
  presented token) and wired `ChangePassword`/password-reset-confirm to revoke every outstanding
  refresh token for the user, same as the existing admin "Revoke sessions" action.
- **Security**: `users.mfa_totp_secret` stored each user's TOTP secret in plaintext — the one
  credential-class secret in the schema that never went through `secrets.Store`, unlike every
  LLM/MCP/SAML/SMTP credential. It now stores an opaque `secrets.Store` ref instead; a migration
  clears any pre-existing plaintext value (no production deployment exists yet, so the only impact
  is dev/staging MFA enrollments needing to re-enroll).
- **Security**: `deploy/k8s`'s api/ingest/worker pods only set `runAsNonRoot` — added `seccompProfile:
  RuntimeDefault`, `readOnlyRootFilesystem`, `allowPrivilegeEscalation: false`, and
  `capabilities: {drop: [ALL]}`, closing off container-breakout techniques a future dependency CVE
  might otherwise get to use (none of the three binaries write outside their existing volume
  mounts, so read-only root needed no new volumes).
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
