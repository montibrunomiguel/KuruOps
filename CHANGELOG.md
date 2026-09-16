<p align="right"><a href="CHANGELOG.pt-BR.md">🇧🇷 Português</a> · <b>🇺🇸 English</b></p>

# Changelog

Format based on [Keep a Changelog](https://keepachangelog.com/). Dates in YYYY-MM-DD.

Since this project doesn't version releases yet (`v0.x`), the sections below group by development
milestone instead of tag — from now on, every relevant change (a feature, a security fix, a
user-visible behavior change) should get an entry here in the same PR that introduces it, not as
after-the-fact archaeology. See the matching item in `.github/PULL_REQUEST_TEMPLATE.md`'s checklist.

## [Unreleased]

### Fixed

- **Changing your password signed you out on the next page reload.** The change revokes every
  refresh token for the user, which is right -- a password change is exactly when a stolen token
  must stop working -- but it revoked the caller's own cookie too, and nothing replaced it. The user
  kept working on an in-memory access token and was thrown back to the login screen by their first
  reload, with nothing on screen explaining why. This hit *every* new deployment, because the seeded
  admin is forced to change the password before anything else unlocks. A fresh refresh token is now
  issued after the revoke, so the session doing the changing survives and every other device is
  still signed out.

### Security

- **SAML no longer answers the IdP's POST with a session token.** `ServeACS` returned one as raw
  JSON, putting a credential in browser history, in any proxy log along the way, and in the page
  itself if the redirect never happened. The documented fix was for the SPA to exchange a
  single-use code -- but the refresh cookie already *is* such a code, and a stronger one (HttpOnly,
  `SameSite=Strict`, scoped to `/auth`, rotating on every use, revocable). So the ACS sets that
  cookie and redirects to `/login/saml`, where the SPA trades it for an access token through the
  ordinary `POST /auth/refresh`. No token travels in a URL or in a body any script can read.

  The callback route is deliberately not under `/auth/`: nginx proxies that whole prefix to the
  API, so a callback there is answered by the backend router with a 404 and never reaches the SPA.
  Found by opening it in a browser -- nothing in the Go or Vitest suites knows nginx exists.

  `POST /auth/refresh` now returns the user alongside the token. It has to: the access token is
  never stored, so a page load (and a SAML callback, which does not even have a stored user) has
  only the cookie to rebuild a session from.

  **SAML remains unvalidated against a real identity provider.** Metadata, the login redirect and
  assertion rejection have tests; a successful assertion has only ever been exercised with
  generated certificates. `backend/README.md` says so plainly rather than implying the flow is
  proven.

### Fixed

- **A failed AI run consumed the alert's one shot at a full triage analysis.** The first analysis of
  an alert gets the long alert-triage prompt and later ones get a shorter follow-up prompt, but the
  count included runs that had *failed* -- and a 503 from the LLM ("this model is currently
  experiencing high demand") is the most ordinary way a run ends. So the first transient failure
  silently downgraded every retry: the analyst clicked Analyse again, got a thinner answer than the
  alert deserved, and had no way back. Only runs that actually produced an analysis count now.

- **A failed follow-up question destroyed the finished analysis.** Asking a question re-uses the
  completed run, so a provider failure on that turn left the run `failed` -- and the next attempt,
  seeing a failed latest run, started a brand new conversation from scratch. Two clicks after a
  finished triage report, the screen showed nothing but the analyst's own unanswered question, with
  the report reachable only by reading the database. A failed follow-up now restores the run to
  completed with its report intact; the unanswered turn is dropped rather than left hanging in the
  transcript.

- **Provider failures reached the analyst as raw JSON.** The whole response body became the error
  message, and that message is rendered verbatim in the analysis panel -- so a Gemini outage put a
  multi-line JSON blob on screen, and a mistyped Base URL produced a bare "llm provider returned
  404:" that named nothing. 401/403, 404, 429 and 5xx each say what to go and change; anything
  unrecognised still carries the body, truncated.

- **The triage prompt now says how to redact, not just to redact.** Asked to triage an alert whose
  payload carried an API key, a real model reproduced the key verbatim while recommending it be
  rotated -- it read naming the value as being helpful. The instruction now covers that case by
  name. Re-tested against the same payload afterwards: both the key and the password came back as
  `[REDACTED]`. Model compliance is not guaranteed by an instruction, so treat this as reducing the
  risk rather than removing it.

### Fixed

- **An alert or incident whose title carries a long unbroken token pushed the action buttons off
  the screen.** A URL, SHA-256, base64 blob or Windows path in a title has no break opportunity, so
  the heading grew to its full intrinsic width and dragged the header with it: on a 1039px window a
  routine "Malicious download blocked: https://..." alert put Close, Escalate and Analyse at
  x=2391, reachable only by scrolling sideways. The title block now declares a flex basis with
  `min-width: 0`, and the header wraps, so the buttons drop to their own line instead of squeezing
  the title; `overflow-wrap: anywhere` lets the token itself break. Affects all three detail pages
  (alerts, incidents, playbooks), which share `.detail-header`.

- **A webhook payload over the 1 MiB limit was reported as malformed JSON.** The cap was applied
  with `io.LimitReader`, which reports EOF rather than an error, so the body was silently truncated
  and the decoder answered `decode webhook body: unexpected end of JSON input` -- sending
  integrators to debug their own serializer over a request that was merely too big. Now
  `http.MaxBytesReader`, answering 413 with the limit named.

- **Duplicate-name and bad-reference errors surfaced raw Postgres text.** Creating a second user
  with an existing email returned, verbatim,
  `duplicate key value violates unique constraint "users_tenant_email_uq" (SQLSTATE 23505)`, naming
  internal identifiers and telling the admin nothing. Same for roles, webhook endpoints, field
  mapping templates, and for a `roleId` that doesn't exist. `isUniqueViolation` already existed in
  `tag_service.go` and was used in exactly one place; it now lives in `constraint_errors.go`
  alongside `isForeignKeyViolation` and is applied at every create that can hit a constraint.

- **User creation accepted anything as an email.** `not-an-email`, `a@` and `@b.com` all produced
  valid-looking accounts -- and since the address is both the login identifier and the only
  password-reset channel, the mistake only surfaced as "they never got their invite". Both paths
  that write `users.email` (create and profile edit) now call `domain.ValidateEmail`, which is
  deliberately permissive: it catches the typo without rejecting plus-addressing, subdomains or
  anything else real people use.

- **Incident and on-call-schedule list responses serialized `roles` and `overrides` as `null`.**
  Neither is loaded by its list query -- a deliberate cost decision -- but a nil slice marshals to
  `null` while the TypeScript types declare them non-nullable, so nothing warns until a component
  calls `.filter`/`.find` on one. Both are now `[]`. Same trap this codebase already hit twice with
  `Assignees`; the new test asserts `NotNil` rather than `Empty`, since `Empty` passes for nil and
  is why the earlier cases went unnoticed.

- **An MCP server endpoint was accepted whatever it said.** `file:///etc/passwd` saved fine and
  failed much later as a 502 from Discover Tools. The endpoint is now checked for an http(s) scheme
  and a host at save time. This is not the SSRF control -- `httpguard` still refuses private
  addresses at request time, and must, since DNS can point a public-looking name at 127.0.0.1 long
  after this check passes.

### Security

- **Breaking (API clients only).** The refresh token is no longer returned in any response body.
  Every login path -- local, LDAP, MFA's second step, and SAML's ACS -- now sets it as a
  `kuruops_refresh` cookie with `HttpOnly`, `SameSite=Strict`, `Path=/auth`, and a `Max-Age` equal
  to the token's own 30-day TTL. `Secure` follows `APP_BASE_URL`'s scheme, so an https deployment
  gets it and a laptop on `http://localhost` (the one cleartext origin browsers still trust) still
  works. `/auth/refresh` and `/auth/logout` read the token from that cookie only: a `refreshToken`
  field in the request body is ignored, which is what stops a value scraped from anywhere else
  being replayed.

  The access token moves to memory only -- it is no longer written to `localStorage`, so a reload
  recovers the session by exchanging the cookie rather than by reading a stored credential.
  Browser storage now holds nothing but display data (name, email, role label). An already
  logged-in browser is carried across the upgrade: the user is read out of the old
  `{ token, refreshToken, user }` entry and its tokens discarded.

  What this buys is narrow and worth stating precisely. It does not make XSS harmless -- script on
  the page can still call `/auth/refresh` and use the access token it gets back. It stops a
  30-day credential being exfiltrated to an attacker-controlled host, which is the difference
  between damage bounded by the lifetime of the injected script and a portable, month-long
  session.

### Added

- Outbound LLM and MCP calls go through a circuit breaker
  (`backend/internal/circuitbreaker`), keyed by host so one dead provider cannot fail calls to a
  healthy one. After `EXTERNAL_CALL_BREAKER_THRESHOLD` consecutive faults (default 5) calls fail
  immediately for `EXTERNAL_CALL_BREAKER_COOLDOWN` (default 30s), then one probe is let through;
  a failed probe re-opens without re-counting. Either value at 0 restores the previous behaviour.

  Only transport errors, 5xx and 429 count as faults. A 4xx deliberately does not: a wrong API key
  returns 401 on every call, so counting it would open the breaker permanently and replace the one
  error message that tells an operator what to fix with a generic "circuit breaker is open".

### Changed

- `docs/THREAT_MODEL.md` listed tag-scoping on incident sub-resources as an open gap. It isn't:
  comments, links, IOCs, timeline, status history and tool-call approval all load the parent
  through `loadVisible(ctx, tx, id, allowedTags)` first, and
  `TestSubResourceRoutesRejectTagRestrictedCaller` proves it by walking the registered chi routes
  (42 of them) rather than a hand-maintained list. The doc and the matching paragraph in
  `backend/README.md` now describe what the code does. No behaviour change -- this entry exists
  because a stale "known gap" in a threat model is worse than no entry at all.

### Changed

- Major dependency bumps now require review, in three layers. `.github/dependabot.yml` states the
  policy and stops re-proposing `golangci-lint-action`'s major, which is blocked on migrating
  `.golangci.yml` to the v2 schema -- with a note saying what unblocks it, so it is deferred rather
  than closed-and-forgotten. `.github/CODEOWNERS` makes every PR request a review, and calls out
  the paths where an unreviewed change is least visible and most damaging: CI definitions, the
  dependency automation itself, deployment manifests, and the secrets and SSRF-guard packages.
  `CONTRIBUTING.md` writes the policy down along with the questions worth asking of a major -- does
  this package have a paired one that must move together, does it drop something it provided
  transitively, does it change a config format this repository has a file for.

  Neither layer *blocks* a merge on its own. The enforcement is branch protection requiring a
  review, which needs a public repository or a paid plan -- on a private free repo the API refuses
  it outright, which is precisely how fifteen PRs merged unreviewed. Enabling it is a step to take
  when the repository is made public.


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

- Alerts can be closed in bulk. Closing deliberately cannot ride on the existing bulk status
  change -- the repository refuses a direct transition to `closed` so a classification is always
  captured -- which left triaging a burst of near-identical false positives as a one-dialog-at-a-
  time job. Selecting "Closed" in the alert list's bulk toolbar now reveals a classification
  picker and an optional shared note, and `POST /api/v1/alerts/bulk/close` applies them to every
  selected alert. Per-alert outcomes are reported the same way the bulk status change reports
  them, so one already-closed or tag-hidden alert does not stop the rest; an unusable
  classification is rejected once as a bad request rather than failing every alert in the batch
  with the same error. Attachments are deliberately not accepted in bulk -- an attachment is
  evidence about one specific alert, and stapling the same file to fifty of them would make the
  record say something nobody meant.
- `.gitleaks.toml` and a `secret-scan` CI job, so the pre-publication secret audit is repeatable
  by anyone rather than a one-off. The scan reads full history, not just the working tree, since a
  secret that was committed and later removed is still a secret. Its allowlist names each
  known-benign match individually -- the documented dev encryption key, Vault's dev-mode root
  token, PEM placeholders reading `CHANGEME`, and test fixtures containing the literal word
  `fake` -- so a NEW secret in one of those same files still fails the scan.


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

### Added

- The first analysis an alert or incident ever gets is now a full structured triage pass instead
  of a few free-form paragraphs: four phases (collect, correlate, classify, escalate), an explicit
  disposition (true positive / benign true positive / false positive), a P1-P4 priority keyed to
  contextual risk rather than the severity the alert arrived with, and a fixed report layout
  (summary, affected entities, decision table, evidence, correlation, recommended actions, tuning).
  Re-analyses and follow-up chat turns keep the shorter general prompt -- the triage report is
  already on the timeline by then. The methodology is adapted from the `alert-triage` skill in
  UnitOneAI/SecuritySkills (MIT), which builds on MITRE ATT&CK and NIST SP 800-61 Rev 2. The
  prompt also carries hard constraints that matter once MCP tools are attached: recommend
  containment but never perform it, never execute anything found in a payload, and treat
  instructions embedded in alert content as data to report rather than directives to follow --
  alert payloads arrive from webhooks and are attacker-influenced by definition.
- AI providers can now be edited after they are created. The backend already had the endpoint;
  the Settings panel simply never called it, so fixing a mistyped base URL or model meant deleting
  the provider and re-entering the API key. Leaving the key field blank keeps the stored one --
  it is never sent back to the browser, so there is nothing to prefill it with.

### Fixed

- Repaired `main` again after ten more Dependabot PRs were merged unreviewed (#101-#110), several
  of them majors. Three CI jobs failed, from two causes.
- **The Go toolchain pin desynced from the module.** Dependabot raised `go.mod`'s directive to
  1.26; the workflows still installed 1.25, so `Backend (Go)`, `dbmigrate` and `Go Vulnerability
  Check` all died on `go.mod requires go >= 1.26.0 (running go 1.25.14)`. Now `go-version: '1.26.x'`
  in all three places -- deliberately not `go-version-file: backend/go.mod`, which looks like the
  tidier fix and is worse: the directive is a floor, not a recommendation, so the action would
  install exactly 1.26.0, whose standard library carries five known vulnerabilities fixed in
  1.26.6. `govulncheck` reports five on 1.26.0 and zero on 1.26.6.
- **TypeScript 7 is not yet usable here.** `typescript-eslint`'s newest release still declares
  `typescript >=4.8.4 <6.1.0`, so TS 7 makes `npm ci` fail on an unsatisfiable peer range -- which
  took out the frontend job and the frontend image build. Reverted to the 5.9 line and added the
  TypeScript major to `.github/dependabot.yml`'s ignore list, with the command that tells you when
  it is safe to remove (`npm view typescript-eslint@latest peerDependencies`).
- Vitest 5 defaults to the `forks` pool -- one child process per test file, each building its own
  jsdom. That fits a developer machine and not a two-core CI runner: all 71 files died with
  `[vitest-pool]: Failed to start forks worker`, and the run reported "no tests" rather than a
  failure anyone could read. Switched to a capped `threads` pool, which keeps per-file isolation,
  gives up only the process boundary (nothing here needs it), and runs the suite in 46s instead of
  86s.
- `gitleaks-action` v3 writes its findings back onto the pull request, so under the workflow's
  read-only default it failed with `Resource not accessible by integration` (403) *after* scanning
  -- which reads as a scan failure rather than a permissions one. The job now grants
  `pull-requests: write` for itself only.
- The CI Node version desynced from the toolchain the same way the Go one did. Dependabot moved
  `frontend/Dockerfile` to `node:26` but the workflow stayed on Node 20, and Vitest 5 requires
  `^22.12.0 || ^24.0.0 || >=26.0.0` -- so every test file failed to start a worker and the run
  reported "no tests". The message names the pool, not the Node version, which is what made it look
  like a pool problem. CI now runs Node 26, matching the image.


- Repaired `main` after fifteen Dependabot pull requests were merged in one go, several of them
  major-version bumps. The dependency automation added in the previous change worked as designed
  -- majors arrived as separate PRs rather than grouped -- but merging them without review broke
  the build in six distinct ways, each with its own cause: `react` went to 19 while `react-dom`
  stayed on 18, splitting the runtime pair across a major; `@types/react` and `@types/react-dom`
  split the same way; ESLint 10 dropped `@eslint/js` as a transitive dependency and left
  `eslint-plugin-react-hooks@5` unable to satisfy its peer range; `golangci-lint-action` v9 drives
  golangci-lint v2, whose config schema is a rewrite the repository's v1 `.golangci.yml` does not
  match; `arduino/setup-task` without a token exhausted the runner's shared unauthenticated GitHub
  API budget; and `alpine:3.24` shipped `libcrypto3` 3.5.7-r0, carrying an OpenSSL denial of
  service (CVE-2026-14456).
- The backend image now runs `apk upgrade` before installing packages. A base image tag is rebuilt
  on its own schedule, so between an OS package CVE being fixed upstream and the tag being
  republished, every build ships the vulnerable version. The frontend image already did this; the
  backend now matches. All four images scan clean afterwards.
- `golangci-lint-action` is pinned at v6 with a note that a Dependabot PR raising it should be
  closed rather than merged until `.golangci.yml` is migrated to the v2 schema -- that migration is
  its own piece of work, not a side effect of a version bump.

### Changed

- `eslint-plugin-react-hooks` v7 introduces `set-state-in-effect`, `purity` and `refs`, which did
  not exist in v5 and flag 18 places across 14 files -- overwhelmingly the "load existing config
  into form state on mount" pattern. They are set to `warn` rather than adopted or silenced: the
  signal stays visible and new code is still flagged as it is written, without holding the build
  hostage to a refactor nobody has scheduled. Raise them back to `error` once the existing hits are
  cleared.


- Bumped `google.golang.org/grpc` 1.82.1 -> 1.83.1 for CVE-2026-84304 (HIGH), flagged by the
  container image scan on the three Go images. An indirect dependency reached through the Google
  API client; `govulncheck` reports nothing in called code, but it ships inside the images.

- **Every API restart left the frontend serving `502` until it was restarted too.** nginx resolves
  a literal hostname in `proxy_pass` once, at config load, and caches it for the life of the
  process -- so when the api container came back on a different address after a deploy, a crash or
  a scale event, nginx kept dialling the old one and nothing recovered on its own. Observed
  directly: nginx connecting to `172.20.0.6` while the api sat healthy on `172.20.0.5`, presenting
  to users as the whole application being down. The upstream is now named through a variable with
  an explicit `resolver`, which forces per-request resolution. Verified by parking a placeholder
  container on the api's old address to force it onto a new one, then logging in successfully
  through a frontend that was never restarted.
- Gateway failures reached the user as raw HTTP status text -- a login form rendering
  "Bad Gateway", which names neither what happened nor what to do. `502`, `503` and `504` carry no
  `{error}` body because they come from a proxy rather than the API, so they now map to a
  translated "the server is temporarily unreachable" instead of the status line.
- `PUT /alerts/{id}/assignee` silently unassigned an alert when the request body used the wrong
  field name. `analystId` is a pointer so an explicit `null` can mean "unassign", which made a body
  carrying no recognised key indistinguishable from a deliberate one: a client with a typo got
  `204` and a cleared assignee. The key must now be present; `{"analystId": null}` still unassigns.

### Added

- `.github/dependabot.yml` watching Go modules, npm, GitHub Actions and both Dockerfiles. Nothing
  watched dependencies before, which is how `golang.org/x/crypto` sat twenty days behind a
  published fix until a container scan caught it -- and it matters more with the project public.
  Patch and minor updates are grouped into one PR per ecosystem; majors arrive alone so they get
  read properly.


- Field Mapping Template rules could not address an array element. A path like
  `detect.behaviors.0.tactic` -- the shape CrowdStrike, CloudTrail and Wazuh payloads all use --
  resolved to nothing and was skipped silently, indistinguishable from a field the payload never
  carried, so an admin building a template had no way to tell which had happened. An all-digit
  segment now indexes into an array. A digit against an object is still read as a key first, so a
  payload with a literal `"0"` field keeps working; out-of-range and negative indices resolve to
  nothing rather than wrapping. Alert dedup grouping shares the same resolver and gains this too.
- An on-call roster that did not divide evenly into the concurrent-shift count left periods
  half-staffed. With five responders and two concurrent slots the rotation produced groups of
  `[2, 2, 1]`: one period in every three ran with a single analyst, on a schedule configured for
  two, and nothing said so. The last group now wraps back to the start of the roster, so every
  period is fully staffed. The cost -- somebody covers two periods back to back each cycle -- is
  now stated in the schedule editor whenever the roster does not divide evenly, rather than left
  to be discovered from the calendar. The TypeScript rotation used by the timeline preview was
  changed in step, and the shared fixtures in `docs/oncall-rotation-fixtures.json` keep the two
  honest.
- `ALLOW_PRIVATE_NETWORK_TARGETS` could not be set on a shipped deployment. `httpguard`'s own
  refusal message tells the operator to set it, and both `.env.example` files and
  `docs/THREAT_MODEL.md` document it -- but `docker-compose.yml` never referenced it, no service
  declared it, and there is no `env_file:`, so a value in `.env` never reached a container. Same
  for the Kubernetes manifests. It is now declared by the api, ingest and worker services and in
  the ConfigMap, empty by default.
- A failing webhook destination's entire response body was read into an error string with an
  unbounded `io.ReadAll` and echoed into the API response and the logs. The destination is
  admin-configurable and its response is controlled by whoever owns it, so one failed step could
  produce an arbitrarily large error. The body is now capped at 4 KiB and marked truncated.
- An over-maximum page limit returned fewer rows than the maximum. `limit=500` fell through to the
  repository default of 50 rather than clamping to the 200 cap, so asking for more got you less,
  with nothing in the response indicating a cap had been applied. Values above the cap are now
  clamped to it; a non-numeric limit still falls back to the default, since there is no sensible
  number to infer from garbage.

### Added

- `GET /api/v1/settings/on-call-schedules/current` answers "who is on call right now" for every
  schedule, in the tenant's timezone. This was previously unanswerable: on-call resolution existed
  only inside escalation delivery, where it picks one analyst at random to notify, so no analyst,
  status page or paging integration could simply ask -- and the Settings timeline had to
  re-implement the rotation in TypeScript to draw its calendar. Returns the full set rather than a
  single pick, since with concurrent shifts there genuinely are several people on call, and it
  keeps schedules where nobody is on call rather than dropping them: "nobody" is exactly what a
  caller needs to be able to see.

### Changed

- The ingest rate limit's default is raised from 60 to 600 requests per minute, and its keying is
  now documented where it is configured. It is keyed on the **source IP**, not the endpoint or the
  token, so every integration arriving from one address shares one budget -- at 60, a customer
  whose SIEM forwarder or NAT fronts five sources started getting `429` at twelve alerts per
  source per minute. The new default still bounds a flood; lower it if ingest sits on an untrusted
  network.


- **Access control**: the dashboard's alert/incident trend charts and its MTTA/MTTR averages
  ignored tag scoping. Every card beside them was scoped correctly, but the time-series was not:
  two analysts restricted to different, non-overlapping tags received byte-identical trends and
  response-time averages covering the entire tenant. The trends read materialized views keyed on
  `tenant_id` alone, with no tag dimension to filter on, so a tag-restricted caller now takes a
  live path over the base tables that reproduces each view's definition exactly. An unrestricted
  caller keeps the pre-aggregated fast path. What leaked was aggregate metadata rather than
  content -- counts and averages, never a title or a payload -- but for a tenant using tags to
  separate clients or business units, that is precisely what tag separation is bought to prevent.
- Alerts arriving from a webhook with no tags were invisible to every tag-restricted analyst. Tag
  visibility is an intersection, so a record with no tags overlaps with nothing: stand up a SOC
  where Tier 1 is tag-restricted and freshly ingested alerts were visible to nobody who was
  supposed to triage them, with no error and no empty-queue indicator anywhere. An alert whose
  source sends no tags is now tagged with the endpoint's own name, which keeps the fail-closed
  rule intact while making the tag meaningful (it identifies the source) and grantable to a role.
- Playbook keywords were collected in the editor, stored, and never consulted. Matching tested
  only `alert_name_pattern` and `is_default`, so a playbook could list every keyword an analyst
  could think of and still match nothing -- a feature that read as working and silently was not.
  Keywords now take part, matched case-insensitively as substrings, ranked below an explicit
  pattern (the more specific statement of intent) and above the default playbook.
- The generic webhook normalizer matched severity exactly and case-sensitively, so a source
  sending `High` -- which most SIEM and EDR products do -- had every alert rejected with a 400 at
  the door. Severity is now matched case- and whitespace-insensitively, with the spellings other
  products actually emit mapped onto this one's vocabulary (`info`, `warn`, `error`, `crit`,
  `sev1`-`sev4` and friends). An unrecognised value is still rejected rather than guessed at, and
  the error now names the accepted ones.
- The API accepted playbook steps in the `new` phase, which the UI stopped rendering. Such a step
  was stored, counted, and displayed nowhere -- invisible and uneditable in the product. Both
  create and update now refuse it.
- An escalation destination that could never work saved cleanly and only failed when fired, so
  the operator found out during the incident it was configured for. Webhook destinations are now
  checked when saved. The check is advisory by design -- `httpguard`'s dial-time guard remains
  the actual control, since only that one resolves the name at the instant of the request and so
  cannot be defeated by DNS rebinding -- and it honours `ALLOW_PRIVATE_NETWORK_TARGETS`, so a
  genuine on-prem deployment can still point a step at a private address.
- An invalid IOC type was rejected with a message that echoed the bad value and nothing else.
  There are fifteen valid types and no way to discover them from the error; it now names them.
- An on-call schedule with no participants is flagged in the schedule list. It stays a valid
  configuration -- webhook, PagerDuty and Slack steps all fire to a destination regardless of who
  is on call -- but it puts nobody on call and leaves the analyst name/email/phone blank in every
  notification it feeds, which is worth saying out loud rather than leaving to be inferred from a
  responder count of zero.
- The dashboard's MTTA/MTTR card could print an average above a caption saying it was computed
  from nothing ("32h / 4m" over "based on 0 acknowledged, 0 closed"), because the figure and its
  own denominator came from different queries. It shows a dash when the sample is empty.


- A tenant's first AI provider now becomes the default on its own. Until something is marked
  default, every analysis fails with "no LLM provider configured" -- so finishing the form and
  still having nothing work was the normal first experience. Keyed on "no default exists" rather
  than "no providers exist", because deleting the default promotes nobody: without this, a tenant
  could hold several providers and still have no usable one.
- An empty response from an LLM provider was recorded as a successful analysis. The result was an
  analysis marked "completed" with nothing in it: no output for the analyst, no error for the
  operator, and nothing anywhere indicating the provider had returned blank. Empty completions now
  fail the run with the provider's own `finish_reason` in the message, which separates a truncated
  answer (`length` -- typical of a reasoning model spending its whole output budget thinking) from
  a suppressed one (`content_filter`) from an endpoint that simply is not returning content where
  an OpenAI-compatible caller expects it. Hit in practice against Gemini's OpenAI-compatibility
  endpoint. An empty turn that carries a tool call is still valid, since the tool call is that
  turn's output.

- **Data integrity**: escalating an alert to an incident was three independent transactions
  (create the incident, link the alert to it, flip the alert to `escalated`). Any failure after
  the first one left a real partial state behind — a link failure stranded an incident that was
  created but attached to nothing, a status failure left the alert looking un-escalated while
  already carrying an incident — and because the create ran unconditionally, the retry that
  naturally follows a failed request minted *another* incident for the same alert. Double-clicking
  the button did the same. Escalation now runs entirely in one transaction (all three writes
  commit or none do), and a second attempt on an already-escalated alert returns
  `409 Conflict` instead of a duplicate incident. The alert's existing incident link is the
  source of truth for that check, so it holds regardless of how the request arrived — the UI
  hiding the button was only ever a client-side guard, never enforced by the API.
- Incident PDF exports and postmortems read the incident and its four sub-resources (status
  history, comments, linked alerts, IOCs) in five separate transactions — five snapshots, so a
  comment posted mid-generation could land in a document whose status history predated it, plus
  five redundant permission checks for the same row. All five loads now share one transaction.
- **Accessibility**: dialogs had no accessible name. `Modal` rendered a bare `role="dialog"`, so a
  screen reader announced every one of them as just "dialog" with no indication of what had
  opened. `Modal` now *requires* a `label` prop and applies it as `aria-label` — required rather
  than optional on purpose, so the compiler catches the next unlabeled dialog instead of it
  shipping silently.
- Bumped `golang.org/x/crypto` 0.54.0 -> 0.55.0 for CVE-2026-56854 (an authentication bypass in
  `x/crypto/ssh` from unenforced source-address restrictions), flagged CRITICAL by the container
  image scan on all three Go images. KuruOps doesn't use `x/crypto/ssh` at all -- only argon2id
  for password hashing -- so nothing here was exploitable, but the module was in the dependency
  graph and shipped in the images.
- **Accessibility**: the Playbooks list was unreachable by keyboard. Each row was a
  `<div onClick={navigate}>` with no `tabindex` and no `role`, and the row was the *only* way to
  open a playbook — so a keyboard-only or screen-reader user could create and search playbooks but
  never open one (WCAG 2.1.1 Keyboard, Level A). The row now wraps the title in a real `<Link>`
  using the same `row-link-stretch` pattern the Alerts/Incidents tables already use, keeping the
  whole row clickable. The same fix is applied to an incident's linked-alert rows, which had the
  identical pattern.
- **Accessibility**: four form controls had no accessible name at all — an on-call schedule's
  working-hours start/end time inputs (two adjacent `<input type="time">`, indistinguishable to a
  screen reader), its add-responder select, the add-assignee select, and the incident description
  textarea. All four now carry an `aria-label`.
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
