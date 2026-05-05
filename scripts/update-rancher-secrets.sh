#!/usr/bin/env bash
set -euo pipefail

# Canonical entrypoint for syncing agent-factory prod runtime secrets to the admin cluster.
# Mirrors a single canonical keyset from this repo's .env.prod into namespace-specific secret names.
#
# Default targets:
#   agent-factory     -> agent-factory-runtime
#   slack-orchestrator -> slack-orchestrator-runtime
#   makeacompany-ai   -> makeacompany-ai-runtime-secrets
#
# Usage:
#   ./scripts/update-rancher-secrets.sh
#   ENV_FILE=/path/.env.prod ./scripts/update-rancher-secrets.sh
#   TARGET_SECRET_MAP="agent-factory:agent-factory-runtime,slack-orchestrator:slack-orchestrator-runtime" ./scripts/update-rancher-secrets.sh
#   ROLLOUT_AFTER_SECRET_SYNC=true ./scripts/update-rancher-secrets.sh

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

export ENV_MODE="${ENV_MODE:-prod}"
if [[ -z "${ENV_FILE:-}" ]]; then
  export ENV_FILE="${ROOT}/.env.${ENV_MODE}"
fi
if [[ -z "${KUBECONFIG:-}" && -f "${HOME}/.kube/config/admin.yaml" ]]; then
  export KUBECONFIG="${HOME}/.kube/config/admin.yaml"
fi
export KUBE_CONTEXT="${KUBE_CONTEXT:-admin}"
export TARGET_SECRET_MAP="${TARGET_SECRET_MAP:-agent-factory:agent-factory-runtime,slack-orchestrator:slack-orchestrator-runtime,makeacompany-ai:makeacompany-ai-runtime-secrets}"

"${SCRIPT_DIR}/sync-dockerhub-pull-secret.sh"
"${SCRIPT_DIR}/update-runtime-secret.sh"

if [[ "${ROLLOUT_AFTER_SECRET_SYNC:-false}" == "true" ]]; then
  "${SCRIPT_DIR}/rollout-runtime-workloads.sh"
fi

echo "Done. Mirrored runtime secrets from ${ENV_FILE} using TARGET_SECRET_MAP=${TARGET_SECRET_MAP}."
