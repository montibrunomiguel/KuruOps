#!/usr/bin/env bash
# Lints and unit-tests the Prometheus alert rules embedded in deploy/k8s/09-monitoring.yaml.
#
# The rules live inside a ConfigMap, where nothing checks them: a typo in a PromQL
# expression is accepted by `kubectl apply` and only shows up as an alert that never
# fires. This extracts them, runs `promtool check rules`, then runs the cases in
# deploy/k8s/tests/prometheus-rules.test.yml (promtool's own unit-test format: feed it
# series, say which alerts must or must not be firing at a given time).
#
# Needs docker (uses the prom/prometheus image for promtool) and node (to read the
# ConfigMap). Run from anywhere: `bash scripts/check-prometheus-rules.sh`.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# On Git Bash for Windows the shell paths (/c/Users/...) mean nothing to node or docker;
# cygpath -m turns them into C:/Users/... . A no-op elsewhere.
native() { cygpath -m "$1" 2>/dev/null || printf %s "$1"; }
ROOT_N="$(native "$ROOT")"
WORK_N="$(native "$WORK")"

ROOT="$ROOT_N" WORK="$WORK_N" node -e '
const fs = require("fs");
const s = fs.readFileSync(process.env.ROOT + "/deploy/k8s/09-monitoring.yaml", "utf8").replace(/\r\n/g, "\n");
const start = s.indexOf("  kuruops.rules.yml: |\n");
if (start < 0) { console.error("kuruops.rules.yml not found in 09-monitoring.yaml"); process.exit(1); }
const end = s.indexOf("\n---", start);
const body = s.slice(start + "  kuruops.rules.yml: |\n".length, end).split("\n").map((l) => l.replace(/^    /, "")).join("\n");
fs.writeFileSync(process.env.WORK + "/rules.yml", body);
'
cp "$ROOT/deploy/k8s/tests/prometheus-rules.test.yml" "$WORK/rules.test.yml"

IMAGE="prom/prometheus:v2.53.0"
# MSYS_NO_PATHCONV: stop Git Bash on Windows from rewriting the container path.
MSYS_NO_PATHCONV=1 docker run --rm -v "$WORK_N:/w" -w /w --entrypoint promtool "$IMAGE" check rules rules.yml
MSYS_NO_PATHCONV=1 docker run --rm -v "$WORK_N:/w" -w /w --entrypoint promtool "$IMAGE" test rules rules.test.yml
