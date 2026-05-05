#!/usr/bin/env bash
set -euo pipefail

# Restart known deployments so pods reload envFrom values from updated secrets.

KUBE_CONTEXT="${KUBE_CONTEXT:-admin}"
if [[ -z "${KUBECONFIG:-}" && -f "${HOME}/.kube/config/admin.yaml" ]]; then
  export KUBECONFIG="${HOME}/.kube/config/admin.yaml"
fi

kubectl_cmd() {
  if [[ -n "${KUBECONFIG:-}" ]]; then
    kubectl --kubeconfig="$KUBECONFIG" --context "${KUBE_CONTEXT}" "$@"
  else
    kubectl --context "${KUBE_CONTEXT}" "$@"
  fi
}

restart_if_exists() {
  local namespace="$1"
  local deployment="$2"
  if kubectl_cmd -n "${namespace}" get deployment "${deployment}" >/dev/null 2>&1; then
    kubectl_cmd -n "${namespace}" rollout restart "deployment/${deployment}"
    echo "Rollout restart: ${namespace}/${deployment}"
  fi
}

# Existing prod workloads that consume mirrored secrets today.
restart_if_exists "slack-orchestrator" "slack-orchestrator"
restart_if_exists "makeacompany-ai" "makeacompany-ai-backend"
restart_if_exists "makeacompany-ai" "makeacompany-ai-frontend"

# Future agent-factory namespace: restart all deployments if namespace is live.
if kubectl_cmd get namespace agent-factory >/dev/null 2>&1; then
  mapfile -t deps < <(kubectl_cmd -n agent-factory get deployment -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}')
  for dep in "${deps[@]}"; do
    [[ -n "${dep}" ]] || continue
    kubectl_cmd -n agent-factory rollout restart "deployment/${dep}"
    echo "Rollout restart: agent-factory/${dep}"
  done
fi
