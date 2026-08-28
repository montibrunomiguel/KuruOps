<p align="right"><a href="README.pt-BR.md">🇧🇷 Português</a> · <b>🇺🇸 English</b></p>

# KuruOps frontend

React + Vite + TypeScript. Covers **Dashboard, Alerts, Incidents, Playbooks and Settings**
(complete alert management and SOC/SIEM incident response), with internationalization (pt/en),
light/dark theme, and live updates via SSE (`src/api/eventStream.ts`).

## Running locally

```bash
npm install
npm run dev   # :5173, proxies /api and /auth to http://localhost:8080 (cmd/api)
```

Needs the backend's `cmd/api` running (see `backend/README.md`). Login: `admin@kuruops.local` /
`ChangeMe123!` — every new deploy already ships with this admin, seeded by the migration
`db/migrations/0002_seed_default_admin.up.sql`. There's no "company" field at login (KuruOps is single-instance,
"company" only exists as a tag on alerts/incidents) and there's no signup screen — other local
users are created via direct SQL or through the Settings API after the first admin has logged in.

## Structure

- `src/auth/AuthContext.tsx` — session (JWT token + refresh token), persisted in `localStorage`.
  `mustChangePassword` is read directly from the JWT (`must_change_password` claim), not from the
  backend on every render — see `decodeMustChangePassword`. A 401 tries an automatic refresh-token
  exchange before logging out (see `refreshOnce` in `src/api/client.ts`).
- `src/pages/ChangePassword.tsx` + the guard in `App.tsx` — any session with `mustChangePassword`
  is redirected here before any other screen; the backend enforces the same block for real
  (`middleware.RequirePasswordChanged`), so this isn't just UX, it can be trusted.
- `src/api/client.ts` — thin `fetch` wrapper, always requires the token explicitly.
- `src/api/hooks.ts` — `useList` logs out automatically on a refresh failure (expired/invalid
  token).
- `src/api/eventStream.ts` — hook over `GET /api/v1/events/stream` (SSE); Dashboard/Alerts/
  Incidents use it to reload data live instead of polling.
- `src/pages/dashboard/` — three tabs (Alerts/Incidents/Follow-up), each with KPIs, charts
  (`src/components/charts/`) and filters (severity/status/tag/analyst-or-commander/period —
  `src/components/TimeRangeFilter.tsx` covers both presets and a custom date+time range via
  `<input type="datetime-local">`).
- `src/pages/alerts/`, `src/pages/incidents/` — listing with filters/pagination and a detail page
  (timeline, comments, NIST team roles in the case of incidents, AI analysis).
- `src/pages/playbooks/` — procedure library by category/phase.
- `src/pages/settings/` — one file per panel (Webhooks, AI Integration, MCP Servers, Storage,
  SMTP, Users and Roles, Identity Providers, Tags, On-Call Schedules, Incident SLAs, Escalation,
  Audit Export, External Database), all following the same pattern: `useList` to load, a local
  form to create/edit, inline per-row actions for mutations.

## Tests

```bash
npm test              # Vitest, watch mode
npm run test:coverage # with coverage report
```

~40 test files (components + pages), using Testing Library and per-test mocked `fetch` (no real
server) — see any existing `*.test.tsx` as a reference for the pattern when adding a new one.
`npx tsc --noEmit` and `npm run build` remain the type-checking/build verification.

## What's missing

- **Tool discovery via `tools/list`** — done for servers with `transport: "http"`: the "Discover
  tools" button on each server's row (`MCPServersPanel.tsx`) calls
  `POST /settings/mcp-servers/{id}/discover-tools`, which speaks real MCP with the server
  (`backend/internal/mcpclient`) and shows the real tools with checkboxes instead of free text. The
  creation form still asks for tools as comma-separated text because the server needs to exist
  before tools can be discovered on it — discover after saving.
- **No end-to-end tests** — current coverage is unit/component level (mocked `fetch`); there's no
  e2e suite against a real running backend (that's what `task test:smoke`, at the repo root,
  covers at the API/HTTP level, not the UI level).
