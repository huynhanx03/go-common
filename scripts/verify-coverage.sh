#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
profile="${COVER_PROFILE:-}"
test_events="${COVERAGE_TEST_EVENTS_FILE:-}"
report_file="${COVERAGE_REPORT_FILE:-}"
owns_profile=false
owns_test_events=false

if [[ -z "${profile}" ]]; then
  profile="$(mktemp)"
  owns_profile=true
fi
if [[ -n "$report_file" && -z "${test_events}" ]]; then
  test_events="$(mktemp)"
  owns_test_events=true
fi

cleanup() {
  if [[ "$owns_profile" == true ]]; then
    rm -f "$profile"
  fi
  if [[ "$owns_test_events" == true ]]; then
    rm -f "$test_events"
  fi
}
trap cleanup EXIT

minimum="${MIN_TOTAL_COVERAGE:-65.0}"
packages=(./...)
if [[ -n "${COVERAGE_PACKAGES:-}" ]]; then
  read -r -a packages <<< "$COVERAGE_PACKAGES"
fi

cd "${root}"
test_status=0
if [[ -n "$report_file" ]]; then
  if go test -json -short -count=1 -covermode=atomic -coverprofile="${profile}" "${packages[@]}" > "$test_events" 2>&1; then
    test_status=0
  else
    test_status=$?
  fi
  jq -Rr 'fromjson? | select(.Output != null) | .Output' "$test_events"
elif go test -short -count=1 -covermode=atomic -coverprofile="${profile}" "${packages[@]}"; then
  test_status=0
else
  test_status=$?
fi

actual=""
coverage_functions='[]'
cover_output=""
if [[ -s "$profile" ]] && cover_output="$(go tool cover -func="$profile" 2>/dev/null)"; then
  actual="$(awk '/^total:/ {
    gsub("%", "", $3)
    print $3
  }' <<< "$cover_output")"
  if [[ -n "$report_file" ]]; then
    coverage_functions="$(awk '
      /^total:/ { next }
      NF >= 3 {
        location = $1
        sub(/:$/, "", location)
        coverage = $3
        sub(/%$/, "", coverage)
        printf "%s\t%s\t%s\n", location, $2, coverage
      }
    ' <<< "$cover_output" | jq -Rsc '
      split("\n")
      | map(select(length > 0) | split("\t") as $row | {
          location: $row[0],
          function: $row[1],
          coverage_percent: ($row[2] | tonumber)
        })
      | sort_by(.coverage_percent)
      | .[:20]
    ')"
  fi
fi

coverage_met=false
if [[ -n "$actual" ]] && awk -v actual="$actual" -v minimum="$minimum" 'BEGIN {
  exit !(actual + 0 >= minimum + 0)
}'; then
  coverage_met=true
fi

if [[ -n "$report_file" ]]; then
  test_result="success"
  if (( test_status != 0 )); then
    test_result="failure"
  fi

  tests="$(jq -s '
    [.[] | select(.Package? != null)] as $events
    | ([$events[].Package] | unique) as $packages
    | [
        $packages[] as $package
        | ($events | map(select(.Package == $package))) as $package_events
        | {
            package: $package,
            result: (
              if any($package_events[]; .Action == "fail" and .Test == null) then "failure"
              elif any($package_events[]; .Action == "pass" and .Test == null) then "success"
              elif any($package_events[]; .Action == "skip" and .Test == null) then "skipped"
              else "unknown"
              end
            ),
            test_count: ([$package_events[] | select(.Test != null and .Action == "pass")] | length),
            has_tests: any($package_events[]; .Test != null),
            elapsed_seconds: (
              [$package_events[] | select(.Test == null and (.Action == "pass" or .Action == "fail") and .Elapsed != null) | .Elapsed]
              | last // 0
            ),
            coverage_percent: (
              [$package_events[] | select(.Output != null) | .Output | capture("coverage: (?<value>[0-9.]+)%") | .value | tonumber]
              | first // null
            )
          }
      ] as $package_results
    | {
        package_count: ($packages | length),
        test_count: ([$events[] | select(.Test != null and .Action == "pass")] | length),
        failed_count: ([$events[] | select(.Test != null and .Action == "fail")] | length),
        failed_packages: [
          $packages[] as $package
          | select(any($events[]; .Package == $package and .Action == "fail" and .Test == null))
        ],
        packages: $package_results,
        slowest_packages: ($package_results | sort_by(.elapsed_seconds) | reverse | .[:10]),
        slowest_tests: [
          $events[]
          | select(.Test != null and (.Action == "pass" or .Action == "fail") and .Elapsed != null and .Elapsed > 0)
          | {package: .Package, test: .Test, result: .Action, elapsed_seconds: .Elapsed}
        ] | sort_by(.elapsed_seconds) | reverse | .[:10],
        failed_tests: [
          $events[]
          | select(.Test != null and .Action == "fail")
          | {package: .Package, test: .Test}
        ] | .[:30]
      }
  ' "$test_events")"

  mkdir -p "$(dirname "$report_file")"
  jq -n \
    --arg test_status "$test_result" \
    --arg actual "$actual" \
    --arg minimum "$minimum" \
    --argjson coverage_met "$coverage_met" \
    --argjson tests "$tests" \
    --argjson low_coverage_functions "$coverage_functions" \
    '{
      tests: ($tests + {status: $test_status}),
      coverage: {
        total_percent: (if $actual == "" then null else ($actual | tonumber) end),
        minimum_percent: ($minimum | tonumber),
        threshold_met: $coverage_met,
        packages: ([$tests.packages[] | select(.coverage_percent != null)] | sort_by(.coverage_percent)),
        low_coverage_functions: $low_coverage_functions
      }
    }' > "$report_file"
fi

if (( test_status != 0 )); then
  exit "$test_status"
fi
if [[ -z "${actual}" ]]; then
  echo "could not calculate total statement coverage" >&2
  exit 1
fi
if [[ "$coverage_met" != true ]]; then
  echo "total statement coverage ${actual}% is below ${minimum}%" >&2
  exit 1
fi

echo "total statement coverage: ${actual}% (minimum: ${minimum}%)"
