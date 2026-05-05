#!/usr/bin/env bash
set -euo pipefail

# Build one canonical keyset from .env and mirror it into one or more
# namespace/secret pairs using TARGET_SECRET_MAP:
#   "namespace-a:secret-a,namespace-b:secret-b"
#
# The same values are written for each target to keep prod runtime secrets aligned.

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

KUBE_CONTEXT="${KUBE_CONTEXT:-admin}"
if [[ -z "${KUBECONFIG:-}" && -f "${HOME}/.kube/config/admin.yaml" ]]; then
  export KUBECONFIG="${HOME}/.kube/config/admin.yaml"
fi

if [[ -n "${ENV_FILE:-}" ]]; then
  ENV_FILE="${ENV_FILE}"
elif [[ -n "${ENV_MODE:-}" ]]; then
  ENV_FILE="${ROOT}/.env.${ENV_MODE}"
else
  ENV_FILE="${ROOT}/.env"
fi

TARGET_SECRET_MAP="${TARGET_SECRET_MAP:-agent-factory:agent-factory-runtime,slack-orchestrator:slack-orchestrator-runtime,makeacompany-ai:makeacompany-ai-runtime-secrets}"

kubectl_cmd() {
  if [[ -n "${KUBECONFIG:-}" ]]; then
    kubectl --kubeconfig="$KUBECONFIG" --context "${KUBE_CONTEXT}" "$@"
  else
    kubectl --context "${KUBE_CONTEXT}" "$@"
  fi
}

if [[ ! -f "${ENV_FILE}" ]]; then
  echo "Missing env file: ${ENV_FILE}" >&2
  exit 1
fi

set -a
# shellcheck disable=SC1090
source "${ENV_FILE}"
set +a

required_keys=(
  INFERENCE_PROVIDER
  GEMINI_MODEL
  GEMINI_API_KEY
  NATS_URL
  ORCHESTRATOR_NATS_URL
  REDIS_URL
  BACKEND_INTERNAL_SERVICE_TOKEN
  ORCHESTRATOR_SLACK_BOT_TOKEN
  ORCHESTRATOR_SLACK_APP_TOKEN
  JOANNE_SLACK_BOT_TOKEN
  JOANNE_SLACK_APP_TOKEN
  ROSS_SLACK_BOT_TOKEN
  ROSS_SLACK_APP_TOKEN
  ALEX_SLACK_BOT_TOKEN
  ALEX_SLACK_APP_TOKEN
  GARTH_SLACK_BOT_TOKEN
  GARTH_SLACK_APP_TOKEN
  TIM_SLACK_BOT_TOKEN
  TIM_SLACK_APP_TOKEN
  ANNA_SLACK_BOT_TOKEN
  ANNA_SLACK_APP_TOKEN
  MULTIAGENT_BOT_USER_IDS
  SLACK_SIGNING_SECRET
)

for key in "${required_keys[@]}"; do
  val=""
  eval "val=\${${key}:-}"
  if [[ -z "${val}" ]]; then
    echo "Missing required key ${key} in ${ENV_FILE}" >&2
    exit 1
  fi
done

# Keep this list explicit so only known runtime keys are mirrored.
runtime_keys=(
  SHARED_CONTRACTS_DIR
  SKILL_FACTORY_DIR
  SKILL_TOOL_SPECS_DIR
  MEMORY_BANK_FILE
  AGENT_FACTORY_MODE
  EMPLOYEE_ID
  INFERENCE_PROVIDER
  GEMINI_MODEL
  GEMINI_API_KEY
  BYOK_GEMINI_API_KEY
  GEMINI_ENABLE_WEB_RESEARCH
  ORCHESTRATOR_NATS_URL
  MAKEACOMPANY_BACKEND_BASE_URL
  AGENT_FACTORY_ADMIN_BASE_URL
  AGENT_FACTORY_ADMIN_TOKEN
  NATS_URL
  NATS_STREAM
  NATS_FETCH_BATCH
  NATS_FETCH_MAX_WAIT_MS
  ORCHESTRATOR_INGRESS_WORKERS
  REDIS_URL
  BACKEND_INTERNAL_SERVICE_TOKEN
  CAPABILITY_CATALOG_READ_TOKEN
  REQUIRE_CAPABILITY_CATALOG_READ_TOKEN
  COMPANY_CHANNELS_REDIS_URL
  COMPANY_CHANNELS_REDIS_KEY
  CAPABILITY_ROUTING_EVENTS_REDIS_KEY
  ADMIN_CATALOG_TOKEN
  ADMIN_SIGN_IN_ALLOWLIST
  ADMIN_SESSION_TTL_SEC
  SLACK_ORCHESTRATOR_CAPABILITY_CATALOG_URL
  ORCHESTRATOR_DEBUG_BASE_URL
  ORCHESTRATOR_DEBUG_TOKEN
  ONBOARDING_CHANNEL
  GOOGLE_OAUTH_CLIENT_ID
  GOOGLE_OAUTH_CLIENT_SECRET
  RESEND_API_KEY
  PORTAL_AUTH_EMAIL_FROM
  RESEND_MAGIC_LINK_TEMPLATE_ID
  RESEND_CHECKOUT_WELCOME_TEMPLATE_ID
  STRIPE_SECRET_KEY
  STRIPE_WEBHOOK_SECRET
  STRIPE_PRICE_ID_BASE_PLAN
  STRIPE_PRICE_ID_WAITLIST_DEPOSIT
  JOANNE_NATS_DURABLE_NAME
  ROSS_NATS_DURABLE_NAME
  ALEX_NATS_DURABLE_NAME
  GARTH_NATS_DURABLE_NAME
  TIM_NATS_DURABLE_NAME
  ANNA_NATS_DURABLE_NAME
  ORCHESTRATOR_SLACK_BOT_TOKEN
  ORCHESTRATOR_SLACK_APP_TOKEN
  ORCHESTRATOR_BOT_USER_ID
  SLACK_ORCHESTRATOR_BOT_USER_ID
  MULTIAGENT_BOT_USER_IDS
  ORCHESTRATOR_TOOL_INTENT_ROUTING_ENABLED
  JOANNE_SLACK_BOT_TOKEN
  JOANNE_SLACK_APP_TOKEN
  ROSS_SLACK_BOT_TOKEN
  ROSS_SLACK_APP_TOKEN
  ALEX_SLACK_BOT_TOKEN
  ALEX_SLACK_APP_TOKEN
  GARTH_SLACK_BOT_TOKEN
  GARTH_SLACK_APP_TOKEN
  TIM_SLACK_BOT_TOKEN
  TIM_SLACK_APP_TOKEN
  ANNA_SLACK_BOT_TOKEN
  ANNA_SLACK_APP_TOKEN
  SLACK_SIGNING_SECRET
  ROSS_PERSONAL_GH_TOKEN
  ROSS_ORG_GH_TOKEN
  JOANNE_GOOGLE_CLIENT_ID
  JOANNE_GOOGLE_CLIENT_SECRET
  JOANNE_GOOGLE_REFRESH_TOKEN
  JOANNE_GOOGLE_SENDER_EMAIL
)

secret_args=()
for key in "${runtime_keys[@]}"; do
  val=""
  eval "val=\${${key}:-}"
  if [[ -n "${val}" ]]; then
    secret_args+=(--from-literal="${key}=${val}")
  fi
done

if [[ "${#secret_args[@]}" -eq 0 ]]; then
  echo "No runtime keys with values found in ${ENV_FILE}" >&2
  exit 1
fi

IFS=',' read -ra targets <<< "${TARGET_SECRET_MAP}"
if [[ "${#targets[@]}" -eq 0 ]]; then
  echo "TARGET_SECRET_MAP is empty; expected namespace:secret pairs" >&2
  exit 1
fi

applied_count=0
for pair in "${targets[@]}"; do
  pair="$(echo "${pair}" | xargs)"
  [[ -z "${pair}" ]] && continue

  namespace="${pair%%:*}"
  secret_name="${pair#*:}"
  if [[ -z "${namespace}" || -z "${secret_name}" || "${namespace}" == "${secret_name}" ]]; then
    echo "Invalid TARGET_SECRET_MAP entry '${pair}' (expected namespace:secret)" >&2
    exit 1
  fi

  kubectl_cmd get namespace "${namespace}" >/dev/null 2>&1 || kubectl_cmd create namespace "${namespace}"
  kubectl_cmd -n "${namespace}" create secret generic "${secret_name}" "${secret_args[@]}" --dry-run=client -o yaml | kubectl_cmd apply -f -
  echo "Applied ${secret_name} in namespace ${namespace} (${#secret_args[@]} keys)."
  applied_count=$((applied_count + 1))
done

if [[ "${applied_count}" -eq 0 ]]; then
  echo "No target entries were applied from TARGET_SECRET_MAP=${TARGET_SECRET_MAP}" >&2
  exit 1
fi
