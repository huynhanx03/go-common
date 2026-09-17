#!/usr/bin/env bash
set -euo pipefail

export CONNECTOR_NAME="${CONNECTOR_NAME:-mongodb-connector}"
exec "$(dirname "${BASH_SOURCE[0]}")/configure-kafka-connect.sh"
