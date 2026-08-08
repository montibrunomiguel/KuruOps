#!/usr/bin/env bash
# Compares the total coverage from a just-generated coverage.out against the
# stored baseline in backend/coverage-baseline.txt, failing the build if it
# dropped. The baseline is NOT auto-updated here on purpose -- bump it by
# hand (see coverage-baseline.txt's own comment) as part of any PR that
# intentionally raises coverage, so a regression always shows up as a diff
# a reviewer sees, not a silent ratchet-down.
set -euo pipefail

cd "$(dirname "$0")/.."

COVERAGE_FILE="${1:-coverage.out}"
BASELINE_FILE="coverage-baseline.txt"

if [ ! -f "$COVERAGE_FILE" ]; then
  echo "coverage file not found: $COVERAGE_FILE (run the coverage-producing test task first)" >&2
  exit 1
fi

if [ ! -f "$BASELINE_FILE" ]; then
  echo "baseline file not found: $BASELINE_FILE" >&2
  exit 1
fi

baseline=$(grep -oE '^[0-9]+\.[0-9]+' "$BASELINE_FILE" | head -1)
current=$(go tool cover -func="$COVERAGE_FILE" | tail -1 | grep -oE '[0-9]+\.[0-9]+' | tail -1)

if [ -z "$baseline" ] || [ -z "$current" ]; then
  echo "could not parse a coverage percentage (baseline='$baseline' current='$current')" >&2
  exit 1
fi

# Integer-only comparison (awk, not bc -- bc isn't guaranteed to exist on a
# bare Windows/Git-Bash PATH, same reasoning as the --wait use in db:up).
below=$(awk -v c="$current" -v b="$baseline" 'BEGIN { print (c < b) ? 1 : 0 }')

if [ "$below" = "1" ]; then
  echo "coverage regression: $current% < baseline $baseline% (see $BASELINE_FILE)" >&2
  exit 1
fi

echo "coverage OK: $current% (baseline $baseline%)"
