<p align="right"><a href="README.pt-BR.md">🇧🇷 Português</a> · <b>🇺🇸 English</b></p>

# ArgusOps backend

Go implementation of the backend described in the design handoff (`design_handoff_argusops/`) and
the architecture review. Three binaries, one module:

| Command | What it does | Why it's separate |
|---|---|---|
| `cmd/api` | REST for the frontend: alerts, incidents, playbooks, settings | scales/fails independently from ingestion |
| `cmd/ingest` | Receives SIEM/XDR webhooks (`POST /hooks`, token-authenticated) | different load/rate-limit profile than `api` |
| `cmd/worker` | Background jobs: refreshing KPI materialized views (`mv_alert_daily_stats`, `mv_incident_kpis`, `mv_incident_daily_stats`), `incidents.sla_breached` sweep, on-call escalation sweep | keeps a slow LLM call from blocking CRUD; the AI analysis itself fires in a goroutine inside `cmd/ingest` (during alert ingestion), it doesn't go through this worker |

## Running locally

The fastest path is `task deploy:up` from the repo root (see the root `README.md`) — it brings up
Postgres via Docker, applies migrations, creates the `argusops_app` role, and builds/runs the three
binaries in containers.

To run the binaries directly on the machine (no Docker for Go, only for Postgres):

```bash
cp .env.example .env   # adjust DATABASE_URL after creating the argusops_app role (see db/README.md)
export $(cat .env | xargs)
make run-api      # :8080
make run-ingest   # reuses HTTP_ADDR -- run in separate processes/terminals with different ports
make run-worker
```

Every first deploy already ships with a default admin (`db/migrations/0002_seed_default_admin.up.sql`) —
`admin@argusops.local` / `ChangeMe123!`, with `must_change_password=true`. Testing the local login
(`AUTH_MODE=dev` is enough — no need to generate JWT keys or switch to `dev-headers`; login is real
authentication even in dev mode, only key generation becomes ephemeral):

```bash
curl -X POST http://localhost:8080/auth/login \
  -d '{"email":"admin@argusops.local","password":"ChangeMe123!"}'
# -> {"token":"...", "user": {"mustChangePassword": true, ...}}

# with mustChangePassword=true, EVERY other endpoint under /api/v1 responds 403
# (middleware.RequirePasswordChanged) except this one:
curl -X POST http://localhost:8080/api/v1/account/change-password \
  -H "Authorization: Bearer <token>" \
  -d '{"currentPassword":"ChangeMe123!","newPassword":"<your password>"}'
# -> {"token":"..."}  (new token, without mustChangePassword now)

curl -H "Authorization: Bearer <token>" http://localhost:8080/api/v1/alerts
```

To create other local users (there's no signup endpoint — see
`internal/repository/user_repository.go`), generate the `password_hash` with
`go run ./cmd/hashpw '<password>'`, never write the hash by hand, and insert it via SQL against the
single tenant (`select id from tenants` — always one row, see `TenantRepository.GetDefault`).

ArgusOps is single-instance software (see the architecture review): there's no concept of a
"company"/tenant at login — `tenant_id` still runs through everything under the hood (to allow real
multi-tenant SaaS in the future without rewriting the schema), but the API always resolves the
single tenant automatically. "Company" only exists as a tag on alerts/incidents (the user's
`allowedTags`), not as a login boundary.

To curl `/api/v1` without going through login (a shortcut, not a general-purpose "fake auth" — it
swaps JWT verification for trusting raw headers, so a login token doesn't work in this mode):

```bash
AUTH_MODE=dev-headers make run-api
curl -H "X-Tenant-ID: <uuid>" -H "X-User-ID: <uuid>" http://localhost:8080/api/v1/alerts
```

Testing the ingestion endpoint (create a test `webhook_endpoints` row first, hash the token with
sha256):

```bash
curl -X POST http://localhost:8081/hooks \
  -H "X-Webhook-Token: <token>" \
  -d '{"title":"Multiple Failed SSH Login Attempts","severity":"critical"}'
```

## What's implemented vs. still design

**Implemented**: complete schema (`db/migrations`) with per-tenant RLS, and full CRUD with business
rules for:
- **Alerts** — lifecycle (open → investigating/escalated → closed, classification only on close),
  append-only audit trail (`alert_events`), custom metadata accepted in the webhook payload
  (arbitrary key/value list — Slack channel, external playbook link, etc., rendered in a dedicated
  panel on the detail page), AI analysis triggered automatically on ingestion when an LLM provider
  is configured (same result as the manual "Analyze with AI" button)
- **Incidents** — NIST phases with free jumping + "skipped phase" detection, severity×priority
  matrix, auditable timestamp correction (never overwrites the original value), Team Notes,
  correlated alerts, NIST 800-61 team roles (Commander, Technical Lead, Incident Handler(s),
  Communications Lead, Privacy Officer — `domain.Incident.Roles`, replaced the generic "owners"
  panel on the detail screen)
- **Playbooks** — CRUD + keyword auto-match (with a "General Security Event" fallback)
- **Settings**: webhook endpoints (hashed token + rotation), per-tenant LLM providers (generic
  `kind=openai_compatible`, key never persisted in plaintext — see `internal/secrets/store.go`;
  validated live against Gemini's real OpenAI-compatible endpoint,
  `generativelanguage.googleapis.com/v1beta`, without needing any dedicated adapter — "Analyze with
  AI" works end-to-end with a real provider, not just mocked), MCP servers (tool allow-list + a list
  of side-effecting tools that always require approval — see `service.EvaluateToolInvocation`),
  users/roles and LDAP/SAML group → role/tags mapping (with a button to remove a configuration, in
  addition to create/update), evidence storage integration (S3/GCS — `S3Store` validated live
  against a real bucket: uploading an image via `POST /api/v1/uploads/images`, then `GET`-ing it
  back and confirming the same content, with latency consistent with a real network call to AWS,
  not local disk; `GCSStore` not yet validated against a real GCP account), SMTP (email password
  reset), tags, on-call schedules, per-severity×priority incident SLAs, escalation policies
  (PagerDuty/Slack/generic webhook), CEF audit export, and assisted migration to an external
  Postgres (Settings → External Database)
- Webhook ingestion with generic normalization, MTTA/MTTR computed as a materialized view instead
  of client-side
- **Authentication**: local login (argon2id + JWT RS256), LDAP bind (`internal/authn/ldap.go`,
  using the two-bind pattern: a service account to find the DN, then binding as the user themselves
  to check the password), and SAML SSO (`internal/authn/saml.go`, SP via `crewjam/saml`, with replay
  protection via an `InResponseTo` cookie). All three converge in `AuthService`/`ProvisionFederated`,
  which applies `auth_group_mappings` (IdP group → role/resource_access/allowed_tags) and issues the
  same JWT. `internal/httpserver/middleware.JWTAuth` verifies that token on `/api/v1/**`.
- **Authorization**: role and scope (`resourceAccess`/`allowedTags`) travel in the JWT
  (`authn.Claims`) and are enforced at two points — `middleware.RequireRole("admin")` blocks all of
  `/settings/**` for non-admins, and `middleware.RequireResourceAccess` blocks `/alerts` or
  `/incidents` entirely based on the user's `resourceAccess`. Within each resource, `allowedTags`
  filters the listing in the SQL query (`tags && $allowedTags`) and is checked again in
  `Get`/`ChangeStatus`/`Close`/`ChangePhase`/`SetSeverityAndPriority`/`UpdateDescription` — see
  `service/access.go` and the comment in `IncidentService` about sub-resources (comments, links,
  timeline) that don't yet repeat this check and rely solely on tenant isolation via RLS.

## Authentication: what's missing for production

The flow works end-to-end (local/LDAP/SAML → JWT → `JWTAuth` middleware), but has known gaps,
deliberately left as TODOs instead of a hidden half-solution:

- **`ServeACS` returns the token as raw JSON** — acceptable for testing the flow, but production
  shouldn't expose a session token in the response of a POST coming from an IdP redirect; the
  correct pattern is for the SPA to exchange a single-use code for a token via a separate
  same-origin call.

Resolved since the last revision of this document: session revocation (refresh tokens in
`refresh_tokens`, see `AuthService.Refresh`/`RevokeSessions` and the "Revoke sessions" button in
Settings → Users), SAML metadata caching (`SAMLAuthService`'s `resolveIDPMetadata`, 1h TTL,
invalidated on config save), per-account rate limiting on login (`AuthHandlers.loginAttempts`, in
addition to the existing per-IP limit), dedicated normalizers for Wazuh/CrowdStrike/GuardDuty
(`internal/ingest/normalize_*.go`, routed by `webhook_endpoints.source` in `Handler.normalizerFor`
— sources without a dedicated adapter still fall through to `genericNormalizer`; none of the three
has been validated against real traffic from the respective vendor, treat them as a starting
point), and a real `secrets.Store` backend beyond the default `PersistentEnvStore` (encrypted with
`SECRETS_ENCRYPTION_KEY`, persisted in the `secret_store` table — survives a process restart,
unlike the old pure in-memory `EnvStore`, which still exists only for use in tests):
`SECRETS_BACKEND=vault` (`VaultStore`, KV v2 engine over direct HTTP, without the official SDK) or
`SECRETS_BACKEND=kms` (`AWSKMSStore`, plain Encrypt/Decrypt, without Secrets Manager) — see
`secrets.NewFromConfig` for the factory switch and each backend's variables. `VaultStore` has
already been validated against a real Vault server in dev mode
(`internal/secrets/vault_store_live_test.go`, `task backend:test:vault`) — a real Put→Resolve
cycle, not just the mock in `vault_store_test.go`. `AWSKMSStore` has not yet been validated against
a real AWS account (needs a real credential, see Phase 3 of the plan history in `docs/history/`).

## MCP Client

`internal/mcpclient` speaks real Model Context Protocol with a registered MCP server — handshake
(`initialize` + `notifications/initialized`), `tools/list` (with pagination), and `tools/call`, over
the "Streamable HTTP" transport (POST JSON-RPC 2.0, with support for a single-event
`text/event-stream` response). Validated against the official reference server
(`@modelcontextprotocol/server-everything`, `internal/mcpclient/live_test.go`, `task backend:test:mcp`)
— handshake, listing, and tool calls pass against a real, independent MCP implementation, not just
this repo's internal mocks. `stdio`/`sse` as transports remain unimplemented (they return a clear
error instead of trying and failing confusingly).

`service.MCPToolService` is the boundary between this client and the access policy:
- `DiscoverTools` connects to the live server and returns the real `tools/list` catalog — this is
  what the "Discover tools" button in Settings → MCP Servers calls, to replace manually typing tool
  names with a real checkbox list.
- `ProposeToolCall` always goes through `EvaluateToolInvocation` first (allow-list). A tool with no
  side effects executes immediately; a tool marked in `side_effecting_tools` sits at
  `ai_tool_calls.status = 'proposed'` until `ApproveToolCall`/`RejectToolCall` — the agent never
  executes a side-effecting tool on its own (see the architecture review, "AI suggests vs AI
  executes"). Endpoints: `GET/POST /api/v1/settings/mcp-servers/tool-calls[/{id}/approve|reject]`.

**Resolved since the last revision of this document**: `AIAnalysisService.runAgentAnalysis` now runs
a real agentic loop (up to `maxAgenticTurns = 5` round-trips with the LLM) and calls
`ProposeToolCall` for every tool the model requests. A tool with no side effects executes
immediately and the result feeds back into the next turn; a tool marked `side_effecting_tools`
pauses the entire run (persisted in `ai_analysis_runs` with status `paused`) until an analyst
approves/rejects it in Settings → MCP Servers → Pending Approvals (new panel,
`MCPServersPanel.tsx`) — `MCPToolService.SetOnToolCallResolved` resumes the run where it left off
via `ResumeAnalysisRun`. The "Analyze with AI" action on the alert/incident detail page triggers
this loop end-to-end, and ingesting an alert also fires the same analysis automatically when an LLM
provider is configured.

### Configuring LDAP/SAML for a tenant

The frontend (`frontend/src/pages/settings/IdentityProvidersPanel.tsx`) already covers this —
Settings → Identity Providers. To test directly against the API without running the frontend:

```bash
# LDAP
curl -X PUT http://localhost:8080/api/v1/settings/identity-providers/ldap \
  -H "Authorization: Bearer <admin-token>" \
  -d '{"host":"ldap.acme.local","port":636,"useTls":true,"bindDn":"cn=svc,dc=acme,dc=local",
       "bindPassword":"...","userBaseDn":"ou=people,dc=acme,dc=local","userFilter":"(mail=%s)",
       "groupAttribute":"memberOf"}'

# SAML -- generates the SP keypair on the first call; download the metadata afterwards from
# GET /auth/saml/metadata and register it with the IdP
curl -X PUT http://localhost:8080/api/v1/settings/identity-providers/saml \
  -H "Authorization: Bearer <admin-token>" \
  -d '{"idpMetadataUrl":"https://idp.acme.com/metadata","acsUrl":"https://argusops.acme.com/auth/saml/acs",
       "spEntityId":"https://argusops.acme.com/auth/saml","groupAttribute":"groups"}'
```

## Code pattern

Each domain resource follows: `internal/domain` (struct + transition rules documented in comments)
→ `internal/repository` (plain SQL via pgx, always inside `db.Pool.WithTenant`) →
`internal/service` (the only layer allowed to write, validates transitions before touching the
repo) → `internal/httpserver/handlers` (decodes the request, calls the service, serializes the
response). See `alert_service.go` / `alert_repository.go` / `handlers/alerts.go` as a reference
when adding incidents/playbooks/settings.
