// Basic load test for KuruOps' highest-traffic real paths: the paginated
// alert list, the dashboard summary, and webhook ingest (the one endpoint
// that receives genuine external traffic, not just analyst browser
// requests). Run via `task perf:smoke` (light, few VUs/seconds) or
// `task perf:load` (heavier, manual/local only -- see Taskfile.yml) --
// both just set env vars and hand off to `docker run grafana/k6`, so no
// local k6 install is required.
//
// Env vars (see Taskfile.yml for how each task sets these):
//   BASE_URL      api's base URL (default http://localhost:8080)
//   API_TOKEN     a personal API token (Profile -> API Tokens) -- required
//                 for the authenticated GET scenarios; without it those two
//                 scenarios are skipped (not failed) so this script still
//                 runs something useful with zero setup.
//   WEBHOOK_URL   ingest's base URL (default http://localhost:8081)
//   WEBHOOK_TOKEN a webhook endpoint's token (Settings -> Webhook Endpoints
//                 -> + Novo Endpoint) -- same "skip, don't fail" treatment
//                 as API_TOKEN when absent.
//   VUS / DURATION virtual users / test duration (defaults below are the
//                  "smoke" sizing; perf:load overrides both to something
//                  heavier).
import http from "k6/http";
import { check, sleep } from "k6";

const BASE_URL = __ENV.BASE_URL || "http://localhost:8080";
const WEBHOOK_URL = __ENV.WEBHOOK_URL || "http://localhost:8081";
const API_TOKEN = __ENV.API_TOKEN || "";
const WEBHOOK_TOKEN = __ENV.WEBHOOK_TOKEN || "";

export const options = {
  vus: Number(__ENV.VUS || 5),
  duration: __ENV.DURATION || "30s",
  thresholds: {
    http_req_failed: ["rate<0.01"],
    http_req_duration: ["p(95)<1000"],
  },
};

export default function () {
  if (API_TOKEN) {
    const authHeaders = { headers: { Authorization: `Bearer ${API_TOKEN}` } };

    const alertsRes = http.get(`${BASE_URL}/api/v1/alerts?limit=20&offset=0`, authHeaders);
    check(alertsRes, {
      "GET /api/v1/alerts is 200": (r) => r.status === 200,
    });

    const statsRes = http.get(`${BASE_URL}/api/v1/dashboard/stats`, authHeaders);
    check(statsRes, {
      "GET /api/v1/dashboard/stats is 200": (r) => r.status === 200,
    });
  }

  if (WEBHOOK_TOKEN) {
    const payload = JSON.stringify({
      title: `Load test alert ${__VU}-${__ITER}`,
      severity: "low",
    });
    const webhookRes = http.post(`${WEBHOOK_URL}/hooks`, payload, {
      headers: {
        "Content-Type": "application/json",
        "X-Webhook-Token": WEBHOOK_TOKEN,
      },
    });
    check(webhookRes, {
      "POST /hooks is 2xx": (r) => r.status >= 200 && r.status < 300,
    });
  }

  sleep(1);
}
