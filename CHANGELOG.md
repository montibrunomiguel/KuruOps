<p align="right"><a href="CHANGELOG.pt-BR.md">🇧🇷 Português</a> · <b>🇺🇸 English</b></p>

# Changelog

Format based on [Keep a Changelog](https://keepachangelog.com/). Dates in YYYY-MM-DD.

Since this project doesn't version releases yet (`v0.x`), the sections below group by development
milestone instead of tag — from now on, every relevant change (a feature, a security fix, a
user-visible behavior change) should get an entry here in the same PR that introduces it, not as
after-the-fact archaeology. See the matching item in `.github/PULL_REQUEST_TEMPLATE.md`'s checklist.

## [Unreleased]

### Added

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
  ArgusOps to make that request for them (SSRF). All three now dial through a new
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
