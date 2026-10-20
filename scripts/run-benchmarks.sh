#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${root}"

packages=(
  "./pkg/logger"
  "./pkg/correlation"
  "./pkg/common/http/..."
  "./pkg/common/websocket"
  "./pkg/common/cache/..."
  "./pkg/mq/batcher"
  "./pkg/hash"
  "./pkg/mq/forge"
)

go test -run '^$' -bench . -benchmem -count="${BENCH_COUNT:-5}" "${packages[@]}"
