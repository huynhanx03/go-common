#!/usr/bin/env bash
set -euo pipefail

: "${CONNECTOR_NAME:?set CONNECTOR_NAME to the Kafka Connect connector name}"
: "${CONNECTOR_CONFIG_FILE:?set CONNECTOR_CONFIG_FILE to a JSON config file}"

connect_url="${CONNECT_URL:-http://127.0.0.1:8083}"
maximum_config_bytes=$((1 << 20))

if [[ ! "${CONNECTOR_NAME}" =~ ^[A-Za-z0-9._-]{1,249}$ ]]; then
  echo "CONNECTOR_NAME must be 1-249 ASCII letters, digits, dots, underscores, or hyphens" >&2
  exit 2
fi
if [[ "${connect_url}" != http://* && "${connect_url}" != https://* ]]; then
  echo "CONNECT_URL must use http or https" >&2
  exit 2
fi
if [[ ! -f "${CONNECTOR_CONFIG_FILE}" ]]; then
  echo "connector config file does not exist: ${CONNECTOR_CONFIG_FILE}" >&2
  exit 2
fi

config_bytes="$(wc -c <"${CONNECTOR_CONFIG_FILE}")"
if (( config_bytes <= 0 || config_bytes > maximum_config_bytes )); then
  echo "connector config must contain 1-${maximum_config_bytes} bytes" >&2
  exit 2
fi

# PUT is idempotent: Kafka Connect creates or replaces the named connector.
# Keep credential-bearing config files outside this repository with restricted
# filesystem permissions.
curl \
  --fail-with-body \
  --show-error \
  --silent \
  --request PUT \
  --header "Accept: application/json" \
  --header "Content-Type: application/json" \
  --data-binary "@${CONNECTOR_CONFIG_FILE}" \
  "${connect_url%/}/connectors/${CONNECTOR_NAME}/config"

printf '\nconnector %s configured successfully\n' "${CONNECTOR_NAME}"
