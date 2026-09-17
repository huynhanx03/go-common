#!/usr/bin/env bash
set -euo pipefail

packages=(
  "./pkg/database/elasticsearch"
  "./pkg/database/mongodb"
  "./pkg/database/redis"
  "./pkg/database/widecolumn"
  "./pkg/mq/kafka"
)

for package in "${packages[@]}"; do
  echo "==> integration: ${package}"
  GO_COMMON_INTEGRATION=1 go test "${package}" -run TestClient_Integration -count=1
done
