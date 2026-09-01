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
