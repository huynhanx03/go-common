#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
profile="${COVER_PROFILE:-}"
if [[ -z "${profile}" ]]; then
  profile="$(mktemp)"
  trap 'rm -f "${profile}"' EXIT
fi

minimum="${MIN_TOTAL_COVERAGE:-65.0}"

cd "${root}"
go test -short -covermode=atomic -coverprofile="${profile}" ./...

actual="$(go tool cover -func="${profile}" | awk '/^total:/ {
  gsub("%", "", $3)
  print $3
}')"
if [[ -z "${actual}" ]]; then
  echo "could not calculate total statement coverage" >&2
  exit 1
fi
if ! awk -v actual="${actual}" -v minimum="${minimum}" 'BEGIN {
  exit !(actual + 0 >= minimum + 0)
}'; then
  echo "total statement coverage ${actual}% is below ${minimum}%" >&2
  exit 1
fi

echo "total statement coverage: ${actual}% (minimum: ${minimum}%)"
