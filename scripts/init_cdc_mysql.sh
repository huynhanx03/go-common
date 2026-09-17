#!/usr/bin/env bash
set -euo pipefail

export CONNECTOR_NAME="${CONNECTOR_NAME:-mysql-connector}"
exec "$(dirname "${BASH_SOURCE[0]}")/configure-kafka-connect.sh"
