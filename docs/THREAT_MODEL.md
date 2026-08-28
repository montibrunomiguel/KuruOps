<p align="right"><a href="THREAT_MODEL.pt-BR.md">🇧🇷 Português</a> · <b>🇺🇸 English</b></p>

# Threat Model

This document describes ArgusOps's trust boundaries, what each layer of defense guarantees
(and what it explicitly does not guarantee), and how to rotate a secret without downtime. It's a
complement to the code, not a substitute — where the two diverge, the code is right and this
document is stale; update it in the same PR that changes the behavior described here (see the
checklist in `.github/PULL_REQUEST_TEMPLATE.md`).

## Trust boundaries

```
Browser (untrusted)
   │ HTTPS (production) / HTTP (local dev)
   ▼
nginx (frontend) -- serves the SPA, proxies /api and /auth
   │ internal Docker Compose network
   ▼
api / ingest (Go, trusted) -- authentication and authorization are checked here
   │ argusops_app role, no BYPASSRLS
   ▼
Postgres -- RLS by tenant_id
```

- **Browser → nginx**: the genuinely untrusted boundary. Anything coming from here — header,
  cookie, body — is treated as potentially hostile by the backend.
- **nginx → api/ingest**: nginx is the only thing that talks to the browser; it unconditionally
  rewrites `X-Real-IP` before forwarding (`proxy_set_header X-Real-IP $remote_addr`), so that's
  the only IP header the backend trusts (see `middleware.getClientIP`/`ClientIPFromHeader` —
  never `X-Forwarded-For`, which the client controls directly).
- **`ingest` is a separate surface from `api`**: it's the only one that receives third-party
  webhook traffic (SIEM/XDR vendors), authenticated by a per-endpoint token whose SHA-256 hash is
  stored (`webhook_endpoints.token_hash`), not by a user JWT. Isolating this path limits the blast
  radius of a compromised/leaked ingestion endpoint to that single endpoint (token rotation
  available under Settings → Webhook Endpoints), without touching anything else in the API.
- **`worker` has no HTTP surface at all** — it only runs internal cron jobs (materialized view
  refresh, SLA/escalation sweep), so it isn't a network target.
- **api/ingest/worker → Postgres**: all three connect as the same least-privilege role
  (`argusops_app`, see `db/init/argusops_app_role.sql`) — no `BYPASSRLS`, no table ownership. The
  `postgres` role (superuser, used only by migrations/`task db:*`) bypasses RLS entirely, on
  purpose — it's never the role a user request uses.

## OAuth redirect/callback trust boundary

Google Drive and Slack are connected via a standard authorization-code OAuth flow
(`internal/service/storage_config_service.go`'s `HandleGDriveOAuthCallback`,
`internal/service/slack_config_service.go`'s `HandleOAuthCallback`), both landing on
`OAuthCallbackHandlers` (`internal/httpserver/handlers/oauth_callback.go`) — the one deliberately
unauthenticated top-level route family in this app (`/auth/oauth/...`, not under `/api/v1`), since
a provider's redirect is a plain browser GET with no JWT to attach.

- **`state` is the entire trust anchor.** There is no session at this point — the callback proves
  nothing about who's making the request except whatever the `state` param's matching
  `oauth_states` row (`internal/repository/oauth_state_repository.go`) says: a single-use,
  DB-backed, TTL'd token minted when the flow was started from an authenticated Settings page.
  Anyone who can guess or intercept a valid `state` value before it's consumed could complete the
  flow in the victim's place; it's opaque, generated with `crypto/rand`, and consumed exactly once
  to close that window.
- **The `code`→token exchange happens server-side only** — the callback never trusts anything the
  browser claims about the outcome, only what Google/Slack's own token endpoint returns for the
  `code` this handler received directly.
- **Errors from either provider are echoed back verbatim** (`?gdrive_error=access_denied`, etc) —
  that's provider-supplied text describing a user-facing outcome (declined consent, expired code),
  not this app's own internal state. A failure *inside* `HandleGDriveOAuthCallback`/
  `HandleOAuthCallback` (a DB error, a state-lookup failure, a malformed token response) is logged
  server-side only and redirects with a fixed generic code (`?gdrive_error=connection_failed`) —
  the raw Go error text is never placed in a redirect URL, which browser history, `Referer`
  headers, and any proxy access log along the path could otherwise capture.
- **The tenant is always resolved the same way** (`AuthService.ResolveDefaultTenant`), matching
  every other unauthenticated entry point in this single-tenant app (the SAML ACS endpoint, the
  seeded default admin) — there's no tenant ambiguity for an attacker to exploit at this boundary.

## What Row-Level Security guarantees — and what it doesn't

RLS filters every query by the session's `tenant_id` (`set_config('app.tenant_id', ...)`, see
`db.Pool.WithTenant`). Today ArgusOps runs as a single instance — there's no concept of a
"company"/tenant at login (root `README.md`) — so in current practice, RLS by `tenant_id` is
defense in depth against a query bug that "forgot" the right filter, not the mechanism that
separates users from one another day to day. It exists ready for a real multi-tenant SaaS future
without a schema rewrite.

**RLS does not guarantee**:
- **Fine-grained authorization within a tenant** — that's `role`/`resourceAccess`/`allowedTags`,
  enforced at the service/handler layer (`middleware.RequireRole`,
  `middleware.RequireResourceAccess`, the `tags && $allowedTags` filter in the query), not in the
  RLS policy itself. A bug in that layer is not covered by RLS.
- **Isolation against the `postgres` role** — intentional; that role is only used for
  administrative setup, never by an HTTP request.
- **Consistency of incident sub-resources** — comments, alert links, and the timeline still don't
  repeat the `allowedTags` check that `Get`/`ChangeStatus`/`Close`/`ChangePhase`/
  `SetSeverityAndPriority`/`UpdateDescription` already perform (see `backend/README.md`, section
  "Authorization") — they rely only on tenant isolation via RLS. In a single-tenant environment
  this doesn't leak anything across different tenants, but it means a user with access to the
  `incidents` resource (but without the specific tag on a given incident) can, today,
  comment on/view sub-resources of an incident outside their `allowedTags` if they know the ID.
  Known gap, not a new finding from this review.

## What the data retention sweep guarantees — and what it doesn't

Settings → Data & Audit → Retention configures how long a **closed** alert/incident stays in
ArgusOps before `cmd/worker`'s hourly `sweepDataRetention` job permanently deletes it (default 18
months, separately configurable per resource type — `internal/service/retention_config_service.go`).

- **Deletion is hard, not soft-archive, and irreversible.** There is no undelete, no trash, no
  export-on-delete step — a row past its configured retention window is gone once the sweep runs.
  An admin who wants a copy of what will be purged needs to export it first (Settings → Data &
  Audit → Audit Export) before lowering a retention period below an existing record's age.
- **Only `closed_at` gates eligibility** — an open alert/incident is never touched no matter its
  age, and an incident reopened after closing (clearing `closed_at`) is excluded even if it was
  eligible moments earlier: the sweep selects and deletes each resource type in one atomic
  `WITH ... FOR UPDATE ... DELETE` statement, so Postgres re-validates eligibility against each
  row's current committed state, not a snapshot taken before the delete.
- **Evidence in blob storage is never touched.** Alert/incident comment attachments live in
  whatever storage backend a tenant has configured (S3/GCS/Google Drive/local disk), referenced by
  URL/key — `internal/blobstore.Store` has no `Delete` method anywhere in this codebase, so a purge
  here can only ever remove ArgusOps's own database record, never the underlying file. This is a
  known, deliberate scope limit, not an oversight: cleaning up orphaned blob storage is unimplemented.
- **Who can configure it**: same admin-only gate as every other Settings panel
  (`middleware.RequireRole("admin")`) — lowering a retention period is effectively a
  data-destruction action available to anyone with that role, which is why the frontend gates a
  *lowered* value behind an explicit inline confirmation (raising a value, or saving for the first
  time, needs no confirmation since neither can delete anything that wasn't already going to be
  deleted).
- **Blast radius per tick is bounded** (`retentionSweepBatchLimit`, 5000 rows per resource type per
  hourly tick) — a large backlog on first deploy of this feature is worked off incrementally across
  ticks instead of one unbounded transaction competing with live traffic for the same tables.

## Secrets: how they never travel in plaintext to Postgres

`secrets.Store` (`Put`/`Resolve`) is the only way application code handles a third-party
credential (LDAP bind password, SAML SP private key, LLM API key, S3/GCS credential). The default
backend, `PersistentEnvStore`, encrypts with AES-256-GCM (`SECRETS_ENCRYPTION_KEY`, an environment
variable — never stored in Postgres) before writing to the `secret_store` table; a dump or leak of
the database alone exposes nothing in plaintext, only if the encryption key is also compromised.
`VaultStore`/`AWSKMSStore` (`SECRETS_BACKEND=vault|kms`) go further — the secret never even exists
in Postgres, encrypted or not.

What this does **not** cover: the plaintext value passes through the Go process's memory at the
moment of `Put`/`Resolve` (unavoidable — it's needed to actually bind to LDAP, call the LLM API,
etc.), and it travels from the browser to `api` as plaintext at the moment it's saved in Settings
(HTTPS in production protects that leg; in local dev via `task deploy:up` it's plain HTTP,
acceptable only on a local machine).

**Deliberate tradeoff**: the original `EnvStore` (purely in-memory) never touched disk, but it
lost every credential on every process restart while the rows in the database kept referencing
the old ref — a real availability bug, discovered during live LDAP/SAML testing in this session.
Switching to `PersistentEnvStore` trades "never touches disk" for "survives a restart," mitigated
by AES-256-GCM encryption before the write — it isn't a free fix, it's a durability-vs-surface
tradeoff, documented here on purpose.

**The `.env.example` default key is refused outside dev mode.** `.env.example` ships a real,
working `SECRETS_ENCRYPTION_KEY` so `docker compose up` works out of the box for local dev — the
same convenience makes it a known, publicly-visible key if it's ever copy-pasted straight into a
real deployment instead of generated fresh. `secrets.NewFromConfig` hard-fails at startup if the
configured key still equals that exact value and `AUTH_MODE` isn't `dev`/`dev-headers`.

## Outbound requests to admin-configured URLs: SSRF protection

Five settings accept a URL that this codebase then dials on the tenant's behalf: an escalation
policy's webhook `Destination` (`internal/notifier/webhook.go`), an escalation policy's Slack
channel `Destination` — an admin-pasted Slack "Incoming Webhook" URL, never actually validated to
be a real `hooks.slack.com` address (`internal/notifier/slack.go`), an MCP server's `endpoint`
(`internal/mcpclient/jsonrpc.go`), an LLM provider's `base_url` for the
`openai_compatible`/`azure_openai`/`self_hosted` kinds (`internal/llmclient/llmclient.go`) —
anthropic's fixed `api.anthropic.com` isn't user-configurable, so it isn't in scope here — and a
SAML identity provider's metadata URL (`internal/authn/saml.go`'s `ResolveIDPMetadata`). Anyone
with Settings access to those areas can otherwise point them at `http://169.254.169.254/...`
(a cloud metadata endpoint) or `http://localhost:5432/...` (an internal service that trusts
requests originating from this process) and get argusops-api/argusops-worker to make that request
for them — a classic SSRF pivot from "can edit config" to "can reach internal-only network". The
Slack and SAML cases were found and closed in a post-hardening-plan audit sweep, after the
original SSRF fix (below) shipped covering only the first three.

All five now dial through `internal/httpguard.NewClient`, whose `Transport.DialContext` resolves
the target host and refuses to connect if any resolved IP is loopback, link-local, or private
(RFC1918/RFC4193) — checked against the IP actually being connected to, not just the URL's
hostname string, so it isn't bypassed by DNS rebinding (a name that resolves to a public IP when
the config is saved but a private one when the request is actually made) or by entering a raw
private IP directly. A genuinely on-prem deployment, where a webhook receiver or MCP server or
self-hosted LLM endpoint legitimately lives in private address space, sets
`ALLOW_PRIVATE_NETWORK_TARGETS=true` to opt the whole process out of this guard.

## SAML `SameSite` cookie: why `RelayState` is the primary channel

`RedirectToIDP` originally relied on a cookie (`SameSite=Lax`) to tie the IdP's response back to
the original `AuthnRequest` (replay/CSRF protection). This broke against any real IdP (Okta, Azure
AD, mocksaml.com) — every spec-compliant IdP returns the assertion via a cross-site POST
(HTTP-POST binding), and a `SameSite=Lax` cookie is never sent on a cross-site POST request by
browser design. The fix: the `AuthnRequest` ID travels in `RelayState` (which every
spec-compliant IdP echoes back verbatim) as the primary channel; the cookie remains as a fallback
just for the same-site case. See `internal/authn/saml.go`'s comment on `samlRequestIDCookie` for
wire-format details.

## HTTP security headers

`middleware.SecurityHeaders` (applied to every `api`/`ingest` response,
`internal/httpserver/router.go`) uses a maximally restrictive CSP (`default-src 'none';
frame-ancestors 'none'`) because these responses are always JSON or plain text, never HTML —
there's no legitimate reason to load a script/style/frame from an API response. `frontend/nginx.conf`
defines a separate, more permissive CSP only for the response that serves `index.html` (the only
place that actually serves HTML) — `style-src 'unsafe-inline'` is necessary because the React tree
uses `style={{...}}` extensively (mainly for charts), not because it's left over from a copied
example; there's no `dangerouslySetInnerHTML` nor inline `<script>` anywhere in the app, so
`script-src` remains strict with no matching exception. `X-XSS-Protection` is deliberately not
set — deprecated, Chrome's own XSS Auditor was removed in 2019 after the auditor itself introduced
bugs; current OWASP guidance is to omit the header, not set a value.

## Runbook: secret rotation without downtime

None of the rotations below tear down already-active sessions — the session JWT doesn't depend on
LDAP/SAML/LLM staying configured the way it was at login time.

### LDAP bind password

1. Generate/update the new password on the LDAP directory side first (the service account, not a
   regular user's).
2. Settings → Identity Providers → LDAP → fill the password field with the new value and save.
   Leaving the field blank keeps the old credential (`SaveLDAPConfig`'s "re-save without a new
   password keeps the current one") — **you must type the new password explicitly** to actually
   rotate it.
3. LDAP logins from this point on use the new password; nothing else needs to restart.

### SP signing keypair (SAML)

There's no dedicated "rotate" button — the only supported way is to remove and reconfigure, since
`SaveSAMLConfig` only generates a new keypair when no prior config exists (see its comment:
reconfiguring without deleting keeps the keypair, on purpose, because the IdP already trusts that
certificate).

1. Settings → Identity Providers → SAML → "Remove configuration".
2. Reconfigure from scratch (same IdP metadata URL/XML) and save — this generates a new pair.
3. Download the new metadata at `GET /auth/saml/metadata` and register it with the IdP in place of
   the old one.
4. **SAML login is unavailable between step 1 and the IdP trusting the new certificate from step
   3** — plan a short window if this is the only login method in use; local/LDAP keep working
   during the window.

### An LLM provider's API key

1. Settings → AI Integration → edit the provider → fill the API key field with the new value and
   save. Same rule as LDAP: leaving it blank keeps the old key.
2. No in-progress analysis session is affected — the key is only resolved at the moment of each
   call to the LLM, not held open across requests.

## Infrastructure secrets: `docker-compose.yml` default credentials

The sections above cover application-level secrets (LDAP, SAML, LLM/MCP) — resolved via
`secrets.Store` and rotatable from the Settings UI without restarting anything.
`docker-compose.yml` has a different, more basic category of secret: the credentials that stand up
the stack itself (the Postgres password, the `argusops_app`/`argusops_worker` role passwords, the
`secrets.Store` encryption key). These **do not** go through the UI — they're read directly from
environment variables at the moment each container comes up.

This whole set ships with a working default value baked into `docker-compose.yml` itself
(deliberately — `docker compose up` with no extra configuration already brings up the entire
stack, without requiring someone just experimenting locally to generate secrets first). The side
effect is that these default values are **public**: they appear in plaintext in this repository,
both in `docker-compose.yml` and in `.env.example` (see repository root). They're acceptable for
running the stack on a single developer's laptop. **They are not acceptable** for any environment
reachable by anyone else — a shared staging machine, a demo environment, anything exposed to an
IP other than `localhost`.

### Rotation before any shared use

1. Copy `.env.example` (repository root) to `.env`.
2. Generate new values for each credential:
   - `POSTGRES_PASSWORD` / `POSTGRES_USER`: any strong password; changing `POSTGRES_USER` also
     requires updating the hardcoded `docker compose exec postgres psql -U postgres ...` calls in
     `Taskfile.yml` (`db:up`, `db:test:up`, `db:backup`, etc. — they don't read the variable, they
     assume `postgres` literally).
   - `ARGUSOPS_APP_PASSWORD` / `ARGUSOPS_WORKER_PASSWORD`: any strong password — they just need to
     match what `db/init/argusops_app_role.sql` / `argusops_worker_role.sql` set up when creating
     the roles (run `task db:reset` after changing, to recreate the roles with the new password).
   - `SECRETS_ENCRYPTION_KEY`: `openssl rand -base64 32`. **Warning**: changing this key after
     secrets already exist in Postgres (`secret_store`) makes them unreadable — generate the final
     key before the first `docker compose up`, not after.
3. `docker compose up -d` again for services to pick up the new values (`api`/`ingest`/`worker`
   follow `${VAR:-default}`, so once it's set in `.env` the override value is what comes up).
4. `.env` is in the repository's `.gitignore` — never `git add -f` it.

Deliberate exception: `VAULT_DEV_ROOT_TOKEN_ID` (service `vault-dev`, profile `tools`) is
hardcoded in `docker-compose.yml`, with no override variable. It's the root token of a Vault
instance in dev mode — in-memory storage, discarded on every restart (see the service's own
comment) — so there's nothing durable there to rotate.
