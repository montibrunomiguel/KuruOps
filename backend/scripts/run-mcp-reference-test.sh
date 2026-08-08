#!/usr/bin/env bash
# Starts the official MCP reference server (Streamable HTTP transport),
# waits for it to come up, runs internal/mcpclient's live integration test
# against it, then stops it -- see internal/mcpclient/live_test.go's doc
# comment for why this validates against a real, independently-implemented
# server instead of another mock of our own.
set -euo pipefail

cd "$(dirname "$0")/.."

PORT=3001
URL="http://localhost:${PORT}/mcp"
LOG_FILE="$(mktemp)"

# Redirected to a file, NOT inherited from this script's own stdout: npx
# spawns node as a child process that outlives a plain `kill` on npx's own
# PID often enough that relying on it is flaky, and if that lingering
# process still held this script's stdout open, any caller piping this
# script's output (e.g. `task ... | tail`) would block forever waiting for
# EOF that never comes -- even though the actual test run below has long
# since finished. A background process holding a *log file* open after this
# script exits is harmless; holding the caller's pipe open is not.
npx -y @modelcontextprotocol/server-everything streamableHttp >"$LOG_FILE" 2>&1 &
SERVER_PID=$!

stop_server() {
  # On Windows/Git Bash, $SERVER_PID (bash's $!) is an MSYS-level PID that
  # does not correspond to the actual native node.exe process npx execs
  # into -- neither `kill $SERVER_PID` nor `taskkill /T /PID $SERVER_PID`
  # reliably reaches it, and it's left holding the port forever. The one
  # thing that's actually true regardless of platform is which PID the OS
  # says is listening on $PORT, so look that up directly and kill it.
  if command -v netstat >/dev/null 2>&1 && command -v taskkill >/dev/null 2>&1; then
    real_pid=$(netstat -ano 2>/dev/null | grep ":${PORT} " | grep LISTENING | awk '{print $NF}' | head -1)
    if [ -n "${real_pid:-}" ]; then
      taskkill //F //PID "$real_pid" >/dev/null 2>&1 || true
    fi
  fi
  kill "$SERVER_PID" 2>/dev/null || true
  pkill -P "$SERVER_PID" 2>/dev/null || true
  rm -f "$LOG_FILE"
}
trap stop_server EXIT

echo "waiting for the MCP reference server on :${PORT}..."
for _ in $(seq 1 30); do
  # curl (without -f) only fails non-zero on connection refused, not on an
  # HTTP error status -- a bare GET against /mcp returns 400 (no JSON-RPC
  # body), which is still a real response and proves the server is up.
  if curl -s -o /dev/null "$URL"; then
    break
  fi
  sleep 1
done

TEST_MCP_SERVER_URL="$URL" go test ./internal/mcpclient/... -run TestMCPClient_Live -v
