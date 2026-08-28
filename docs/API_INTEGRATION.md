<p align="right"><a href="API_INTEGRATION.pt-BR.md">🇧🇷 Português</a> · <b>🇺🇸 English</b></p>

# API & Webhook Integration Guide

KuruOps exposes two separate HTTP surfaces, on two separate services, for two separate audiences:

| Surface | Service | Port (local) | Audience | Spec |
|---|---|---|---|---|
| REST API (`/api/v1/**`, `/auth/**`) | `cmd/api` | `:8080` | The frontend, and any admin tooling you build against KuruOps itself | [`docs/openapi.yaml`](openapi.yaml) — the full, authoritative contract |
| Webhook ingest (`/hooks`) | `cmd/ingest` | `:8081` | Your SIEM/XDR/alerting sources, pushing alerts *into* KuruOps | This document |

This document covers the second one: **how an external system sends an alert into KuruOps**. It's deliberately narrative — `openapi.yaml` doesn't cover `/hooks` at all, because its request shape isn't one fixed schema (see "Payload shape by source" below), which doesn't fit OpenAPI's per-path single-schema model well. Everything about the REST API itself (listing/updating alerts, managing settings, authentication) is fully specified in `openapi.yaml`; don't duplicate it here.

## 1. Create a webhook endpoint

Before any alert can be ingested, an admin registers a webhook endpoint in **Settings → Webhook Endpoints** (or via `POST /api/v1/settings/webhooks` on the REST API, same effect). Two fields matter for what happens next:

- **`name`** — a label, purely for your own reference in the Settings list.
- **`source`** — picks which payload normalizer `/hooks` uses for alerts sent through this endpoint (see §3). Case-insensitive; anything that isn't a recognized name falls back to the generic normalizer.

The response includes a bearer **token**, shown exactly once:

```json
{
  "endpoint": { "id": "...", "name": "Wazuh Prod", "source": "wazuh", "tokenLast4": "a1b2", "status": "active", "expiresAt": "2026-11-19T00:00:00Z" },
  "token": "whk_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
}
```

Store that token in your SIEM/sender's configuration now — KuruOps never shows it again (only the endpoint's `tokenLast4`, for recognizing which token is which in the UI). If you lose it, regenerate a new one from Settings (`POST /api/v1/settings/webhooks/{id}/regenerate`) — the old one stops working the moment you do.

By default, a token expires 90 days after it's issued or last regenerated; set `expiresInDays` explicitly at creation (0 or negative = never expires) if that default doesn't fit your rotation policy.

## 2. Send an alert

```
POST http://<ingest-host>:8081/hooks
X-Webhook-Token: whk_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
Content-Type: application/json

{ ... payload, shape depends on the endpoint's configured source, see §3 ... }
```

- **Auth**: the `X-Webhook-Token` header, set to the exact token from endpoint creation. Nothing else authenticates this endpoint — it's a single shared secret per endpoint, not per-request signing.
- **Body size limit**: 1 MiB. Larger bodies are rejected outright.
- **Rate limit**: 60 requests/minute per source IP by default (`WEBHOOK_RATE_LIMIT_PER_MINUTE`), enforced regardless of which endpoint/token is used from that IP. Past the limit: `429 Too Many Requests`.

### Responses

| Status | Meaning |
|---|---|
| `201 Created` | New alert ingested. Body: `{"id": "<alert-uuid>"}`. |
| `200 OK` | Deduplicated against an existing alert (see §5) — no new row was created. Body: `{"id": "<the-original-alert's-uuid>"}`, same shape either way, so a sender that doesn't care about dedup can treat both the same. |
| `400 Bad Request` | Malformed JSON, or a required field is missing/invalid (see §3 per source) — the body is the error message as plain text. |
| `401 Unauthorized` | Missing/invalid/expired token. |
| `403 Forbidden` | The endpoint exists but is disabled (Settings → Webhook Endpoints → Disable). |
| `429 Too Many Requests` | Rate limit exceeded (see above). |
| `500 Internal Server Error` | Something failed on KuruOps' side — the alert was not ingested; safe to retry. |

## 3. Payload shape by source

Every source variant is normalized down to the same internal shape before an alert is created — `title`, `severity` (one of `critical`/`high`/`medium`/`low`/`informational`), and a handful of optional fields. Which normalizer runs is picked by the endpoint's `source` field from creation, not by anything in the request itself.

### `generic` (the fallback — any unrecognized `source` value lands here too)

```json
{
  "title": "Suspicious login from unrecognized device",
  "severity": "high",
  "external_id": "evt-88213",
  "rule_id": "auth-anomaly-42",
  "asset": "vpn-gateway-01",
  "src_ip": "203.0.113.7",
  "tags": ["vpn", "after-hours"]
}
```

`title` and `severity` are required (severity must be exactly one of the five values above, case-sensitive). Everything else is optional. Use this shape for any source without a dedicated adapter below — write your sender to emit this envelope directly, no need to wait for KuruOps to add native support for your specific tool.

### `wazuh`

```json
{
  "id": "1234567890.123456",
  "rule": { "level": 10, "description": "Multiple authentication failures", "id": "5710", "groups": ["authentication_failures", "pci_dss_10.2.4"] },
  "agent": { "name": "web-01", "ip": "10.0.1.15" },
  "data": { "srcip": "203.0.113.7" }
}
```

`rule.level` (Wazuh's own 0–15+ scale) maps to KuruOps severity: `>=12` critical, `>=9` high, `>=6` medium, `>=3` low, otherwise informational. `rule.groups` becomes the alert's tags (auto-created if new — see §4). `rule.description` becomes the title.

### `crowdstrike`

```json
{
  "event": {
    "DetectId": "ldt:abc123",
    "DetectName": "Process Injection",
    "Severity": 80,
    "SeverityName": "High",
    "ComputerName": "WKSTN-042",
    "LocalIP": "10.0.5.20",
    "Tactic": "Defense Evasion",
    "Technique": "Process Injection"
  }
}
```

`SeverityName` (CrowdStrike's own label — Critical/High/Medium/Low/Informational) is used when present; the numeric 0–100 `Severity` score is the fallback band mapping.

### `guardduty`

```json
{
  "id": "8ab5f8...",
  "type": "UnauthorizedAccess:EC2/SSHBruteForce",
  "title": "SSH brute force attempts against i-0abcd1234",
  "severity": 8.5,
  "resource": { "instanceDetails": { "instanceId": "i-0abcd1234", "networkInterfaces": [{ "publicIp": "203.0.113.50" }] } },
  "service": { "action": { "networkConnectionAction": { "remoteIpDetails": { "ipAddressV4": "198.51.100.9" } } } }
}
```

`severity` is GuardDuty's own 0.1–8.9 float score, banded into the five KuruOps tiers.

Adding a new dedicated normalizer (rather than relying on `generic`) is a backend code change — see `backend/internal/ingest/normalize_*.go` for the pattern (`Normalizer` interface, one adapter file per source) if you're contributing one.

## 4. Tags

Any tag your payload sends (`rule.groups` for Wazuh, `tags` for generic — CrowdStrike/GuardDuty don't currently map any field to tags) is auto-created in the tenant's tag catalog the moment it's first seen — nothing needs to be pre-registered in **Settings → Tags** first. A brand-new tag shows up there immediately afterward, fully manageable (color, delete) like any other. See `TagService.EnsureExist` if you're curious about the exact mechanics; the short version is that this can never leak an alert to an analyst who shouldn't see it — a tag-restricted role only gains visibility once an admin explicitly adds the new tag to that role's allow-list, same as any pre-existing tag.

## 5. Deduplication

An endpoint can be configured (Settings → Webhook Endpoints, or `groupByFields`/`dedupWindowMinutes` at creation) with a list of JSON-path field names — dot notation, e.g. `host.name`, matching whatever the sender's own payload structure is, not the normalized shape. When set:

- A new alert whose values at every one of those paths match an existing alert's, ingested within `dedupWindowMinutes` (default 30) of the original, doesn't create a new row — it increments `duplicateCount` on the existing alert instead, and the response is `200 OK` with the *original* alert's id.
- A payload missing one of the configured fields entirely never dedups against anything — absence never matches absence.
- Leave `groupByFields` empty (the default) to disable dedup for that endpoint entirely — every ingested payload becomes its own alert.

## 6. Metadata

Any top-level `"metadata"` object in the raw payload is stored and surfaced verbatim in the alert's detail view, in its own panel — a Slack channel, a runbook link, an environment tag, whatever key/value pairs your source wants to attach. Malformed metadata (not an object, or the field is simply absent) is silently ignored, never a `400` — it's a best-effort enrichment, not a required field.

```json
{ "title": "...", "severity": "high", "metadata": { "slackChannel": "#incident-response", "environment": "production" } }
```

An endpoint can also be configured with a **Field Mapping Template** (Settings → Field Mapping Templates), which pulls additional JSON-path values out of the raw payload and merges them into that same metadata object under labels an admin defines — useful for surfacing a field your source sends that isn't already top-level `metadata`. The sender's own `metadata.*` keys win on a label conflict.

## 7. What happens after ingest

- **AI analysis**: if the tenant's default LLM provider has opted into "Automatically analyze every incoming alert" (Settings → AI Integration), every alert ingested here is analyzed automatically in the background — otherwise, analysis only happens when an analyst clicks "Analyze with AI" in the UI. Either way, ingest itself never waits on this; the response comes back immediately.
- **On-call auto-assign**: if an on-call schedule is configured and matches the alert, it's auto-assigned to whoever's currently on shift.
- **Live update**: any connected browser tab (Dashboard, Alerts list) reflects the new alert within moments via Server-Sent Events — no polling, no manual refresh needed on the receiving end.

## 8. Everything else

Reading alerts back out, updating status, closing/classifying, commenting, escalating to an incident, and every admin/Settings operation all go through the REST API on `cmd/api` (`:8080`), fully specified in [`docs/openapi.yaml`](openapi.yaml). That API requires a real session (local/LDAP/SAML login, or a personal API token from **Profile → API Tokens**) — it's a separate trust boundary from the webhook token above, which can only ever create alerts, nothing else.
