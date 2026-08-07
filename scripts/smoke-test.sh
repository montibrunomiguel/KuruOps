#!/usr/bin/env bash
# End-to-end smoke test against a locally deployed stack (task deploy:up).
# Logs in as the default admin seeded by db/migrations/0013_seed_default_admin.up.sql
# for real over HTTP (no direct SQL seeding of our own -- there's nothing
# left to seed, that's the point of this migration), and exercises a
# handful of routes that only work correctly with the full chain wired up:
# RLS-scoped Postgres, JWT issue/verify, the must-change-password gate, and
# the RequireRole/RequireResourceAccess middleware from
# backend/internal/httpserver/middleware/auth.go.
#
# This is deliberately not exhaustive (it does not touch LDAP/SAML or MCP)
# — it exists to catch "the whole chain doesn't even boot" class of
# regressions cheaply, not to replace real test coverage. Safe to re-run
# against an already-seeded deploy: the default admin's password only
# starts out as DEFAULT_PASSWORD, this script rotates it to ROTATED_PASSWORD
# on its first run and tries both on every run after that.
set -uo pipefail

API_URL="${API_URL:-http://localhost:8080}"
INGEST_URL="${INGEST_URL:-http://localhost:8081}"
EMAIL="admin@argusops.local"
DEFAULT_PASSWORD="ChangeMe123!"
ROTATED_PASSWORD="SmokeTest-Rotated-Pw1!"
# Unique per run so re-running this script against a deploy that already has
# data from a previous run doesn't trip webhook_endpoints_tenant_name_uq.
RUN_ID="$(date +%s)-$$"
WEBHOOK_NAME="Smoke Webhook ${RUN_ID}"

pass=0
fail=0

check() {
  local description="$1" condition="$2"
  if eval "$condition"; then
    echo "  ok   - $description"
    pass=$((pass + 1))
  else
    echo "  FAIL - $description"
    fail=$((fail + 1))
  fi
}

# json_field extracts "key":"value" (or "key":true/false for booleans, via
# the -bool variant) from a JSON blob without needing jq. Never fails the
# script on a miss (returns empty string instead) -- callers check the
# result explicitly so a missing field shows up as a failed check with the
# raw response body printed, not a silent script abort.
json_field() {
  local body="$1" key="$2"
  printf '%s' "$body" | grep -o "\"${key}\":\"[^\"]*\"" | head -1 | cut -d'"' -f4
}
json_bool_field() {
  local body="$1" key="$2"
  printf '%s' "$body" | grep -o "\"${key}\":[a-z]*" | head -1 | cut -d':' -f2
}

login() {
  curl -s -X POST "${API_URL}/auth/login" \
    -H 'Content-Type: application/json' \
    -d "{\"email\":\"${EMAIL}\",\"password\":\"${1}\"}"
}

echo "==> Logging in as the seeded default admin"
login_body=$(login "$DEFAULT_PASSWORD")
CURRENT_PASSWORD="$DEFAULT_PASSWORD"
TOKEN=$(json_field "$login_body" token)
if [ -z "$TOKEN" ]; then
  # Default password didn't work -- likely already rotated by a previous
  # run of this script (see file header). Try the password this script
  # itself would have rotated it to.
  login_body=$(login "$ROTATED_PASSWORD")
  CURRENT_PASSWORD="$ROTATED_PASSWORD"
  TOKEN=$(json_field "$login_body" token)
fi
check "local login as the default admin returns a token" '[ -n "$TOKEN" ]'
if [ -z "$TOKEN" ]; then
  echo "  tried both the default and the rotated password; last response was: $login_body"
  echo
  echo "==> Can't continue without a token. ${pass} passed, ${fail} failed so far."
  exit 1
fi

MUST_CHANGE=$(json_bool_field "$login_body" mustChangePassword)
echo "==> Health checks"
api_health=$(curl -s -o /dev/null -w '%{http_code}' "${API_URL}/healthz")
check "api /healthz returns 200" '[ "$api_health" = "200" ]'
ingest_health=$(curl -s -o /dev/null -w '%{http_code}' "${INGEST_URL}/healthz")
check "ingest /healthz returns 200" '[ "$ingest_health" = "200" ]'

echo "==> Auth"
unauth_status=$(curl -s -o /dev/null -w '%{http_code}' "${API_URL}/api/v1/settings/webhooks")
check "unauthenticated request to a protected route returns 401" '[ "$unauth_status" = "401" ]'

bad_login_status=$(curl -s -o /dev/null -w '%{http_code}' -X POST "${API_URL}/auth/login" \
  -H 'Content-Type: application/json' \
  -d "{\"email\":\"${EMAIL}\",\"password\":\"definitely-wrong\"}")
check "wrong password returns 401 (not 500, not a different status)" '[ "$bad_login_status" = "401" ]'

echo "==> Forced password change"
if [ "$MUST_CHANGE" = "true" ]; then
  locked_status=$(curl -s -o /dev/null -w '%{http_code}' "${API_URL}/api/v1/settings/webhooks" -H "Authorization: Bearer ${TOKEN}")
  check "must-change-password token is rejected everywhere except change-password" '[ "$locked_status" = "403" ]'

  change_body=$(curl -s -X POST "${API_URL}/api/v1/account/change-password" \
    -H "Authorization: Bearer ${TOKEN}" -H 'Content-Type: application/json' \
    -d "{\"currentPassword\":\"${CURRENT_PASSWORD}\",\"newPassword\":\"${ROTATED_PASSWORD}\"}")
  NEW_TOKEN=$(json_field "$change_body" token)
  check "change-password returns a fresh token" '[ -n "$NEW_TOKEN" ]'
  if [ -n "$NEW_TOKEN" ]; then
    TOKEN="$NEW_TOKEN"
  else
    echo "  response was: $change_body"
  fi
else
  echo "  skip - already rotated in a previous run (mustChangePassword is false)"
fi

echo "==> Settings CRUD (RLS + admin-role gate) round trip"
create_body=$(curl -s -X POST "${API_URL}/api/v1/settings/webhooks" \
  -H "Authorization: Bearer ${TOKEN}" -H 'Content-Type: application/json' \
  -d "{\"name\":\"${WEBHOOK_NAME}\",\"source\":\"smoke-test\"}")
WEBHOOK_TOKEN=$(json_field "$create_body" token)
check "create webhook endpoint returns a plaintext token" '[ -n "$WEBHOOK_TOKEN" ]'
[ -z "$WEBHOOK_TOKEN" ] && echo "  response was: $create_body"

list_body=$(curl -s "${API_URL}/api/v1/settings/webhooks" -H "Authorization: Bearer ${TOKEN}")
check "created webhook endpoint appears in the list" 'printf "%s" "$list_body" | grep -q "$WEBHOOK_NAME"'

echo "==> Webhook ingest round trip (tests token-lookup RLS carve-out)"
if [ -n "$WEBHOOK_TOKEN" ]; then
  ingest_status=$(curl -s -o /dev/null -w '%{http_code}' -X POST "${INGEST_URL}/hooks" \
    -H "X-Webhook-Token: ${WEBHOOK_TOKEN}" \
    -d "{\"title\":\"Smoke test alert ${RUN_ID}\",\"severity\":\"informational\"}")
  check "webhook ingest with the real token returns 201" '[ "$ingest_status" = "201" ]'

  alerts_body=$(curl -s "${API_URL}/api/v1/alerts" -H "Authorization: Bearer ${TOKEN}")
  check "ingested alert appears in /api/v1/alerts" 'printf "%s" "$alerts_body" | grep -q "Smoke test alert ${RUN_ID}"'
else
  echo "  skip - no webhook token from the previous step"
fi

echo "==> Not-found handling"
unknown_status=$(curl -s -o /dev/null -w '%{http_code}' "${API_URL}/api/v1/alerts/00000000-0000-0000-0000-000000000000" \
  -H "Authorization: Bearer ${TOKEN}")
check "unknown alert id returns 404, not 500" '[ "$unknown_status" = "404" ]'

echo
echo "==> ${pass} passed, ${fail} failed"
if [ "$fail" -ne 0 ]; then
  exit 1
fi
