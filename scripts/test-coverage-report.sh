#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
temp_dir="$(mktemp -d)"
trap 'rm -rf "$temp_dir"' EXIT

report_file="$temp_dir/coverage-report.json"
log_file="$temp_dir/coverage.log"

if (
  cd "$root"
  COVERAGE_PACKAGES="./pkg/algorithm ./pkg/bytes" \
    COVERAGE_REPORT_FILE="$report_file" \
    MIN_TOTAL_COVERAGE=101 \
    ./scripts/verify-coverage.sh
) > "$log_file" 2>&1; then
  printf '%s\n' 'expected the deliberately high coverage threshold to fail' >&2
  exit 1
fi

if ! jq -e '
  .tests.status == "success" and
  .tests.package_count == 2 and
  .tests.test_count > 0 and
  .tests.failed_count == 0 and
  (.tests.slowest_tests | type) == "array" and
  (.tests.slowest_packages | length) == 2 and
  .tests.slowest_packages[0].elapsed_seconds > 0 and
  .coverage.total_percent != null and
  .coverage.minimum_percent == 101 and
  .coverage.threshold_met == false and
  (.coverage.packages | length) == 2 and
  (.coverage.low_coverage_functions | length) > 0
' "$report_file" >/dev/null; then
  cat "$log_file" >&2
  printf '%s\n' 'coverage report did not include the expected test and coverage measurements' >&2
  exit 1
fi

printf '%s\n' 'coverage report includes package results, test timings, and measured coverage hotspots'
