#!/usr/bin/env bash
set -euo pipefail

# Copy dockerhub-pull into each target namespace in TARGET_SECRET_MAP.

KUBE_CONTEXT="${KUBE_CONTEXT:-admin}"
if [[ -z "${KUBECONFIG:-}" && -f "${HOME}/.kube/config/admin.yaml" ]]; then
  export KUBECONFIG="${HOME}/.kube/config/admin.yaml"
fi

TARGET_SECRET_MAP="${TARGET_SECRET_MAP:-agent-factory:agent-factory-runtime,slack-orchestrator:slack-orchestrator-runtime,makeacompany-ai:makeacompany-ai-runtime-secrets}"
PULL_SECRET_NAME="${PULL_SECRET_NAME:-dockerhub-pull}"
PULL_SECRET_SOURCE_NAMESPACE="${PULL_SECRET_SOURCE_NAMESPACE:-bimross-web}"
PULL_SECRET_FALLBACK_NAMESPACE="${PULL_SECRET_FALLBACK_NAMESPACE:-employee-factory}"

kubectl_cmd() {
  if [[ -n "${KUBECONFIG:-}" ]]; then
    kubectl --kubeconfig="$KUBECONFIG" --context "${KUBE_CONTEXT}" "$@"
  else
    kubectl --context "${KUBE_CONTEXT}" "$@"
  fi
}

source_ns="${PULL_SECRET_SOURCE_NAMESPACE}"
if ! kubectl_cmd get secret "${PULL_SECRET_NAME}" -n "${source_ns}" >/dev/null 2>&1; then
  echo "Pull secret '${PULL_SECRET_NAME}' not found in '${source_ns}', trying '${PULL_SECRET_FALLBACK_NAMESPACE}'..."
  source_ns="${PULL_SECRET_FALLBACK_NAMESPACE}"
  kubectl_cmd get secret "${PULL_SECRET_NAME}" -n "${source_ns}" >/dev/null 2>&1 || {
    echo "Unable to find '${PULL_SECRET_NAME}' in '${PULL_SECRET_SOURCE_NAMESPACE}' or '${PULL_SECRET_FALLBACK_NAMESPACE}'." >&2
    exit 1
  }
fi

IFS=',' read -ra targets <<< "${TARGET_SECRET_MAP}"
for pair in "${targets[@]}"; do
  pair="$(echo "${pair}" | xargs)"
  [[ -z "${pair}" ]] && continue
  namespace="${pair%%:*}"
  [[ -z "${namespace}" ]] && continue

  kubectl_cmd get namespace "${namespace}" >/dev/null 2>&1 || kubectl_cmd create namespace "${namespace}"
  kubectl_cmd get secret "${PULL_SECRET_NAME}" -n "${source_ns}" -o json \
    | python3 -c 'import json,sys; src=json.load(sys.stdin); ns="'"${namespace}"'"; out={"apiVersion":"v1","kind":"Secret","metadata":{"name":src["metadata"]["name"],"namespace":ns},"type":src.get("type"),"data":src.get("data",{})}; print(json.dumps(out))' \
    | kubectl_cmd apply -f -

  echo "Synced '${PULL_SECRET_NAME}' into namespace '${namespace}' from '${source_ns}'."
done
