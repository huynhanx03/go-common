#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${root}"

export LC_ALL=C
export TZ=UTC

required_docs=(
  "docs/migration-v0-to-v1.md"
  "docs/security.md"
  "docs/lifecycle.md"
  "docs/websocket.md"
  "docs/outbox.md"
  "docs/benchmarks.md"
)

for document in "${required_docs[@]}"; do
  if [[ ! -s "${document}" ]]; then
    echo "required documentation is missing or empty: ${document}" >&2
    exit 1
  fi
done

go test ./docs -run '^Example' -count=1
