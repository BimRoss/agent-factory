# agent-factory

Greenfield runtime for MakeACompany employee execution.

## Mission

Keep `slack-orchestrator` as entrypoint and routing envelope, while moving execution intelligence into modular employee runtimes that:

- run Cogito-first planning + tool selection
- perform internal infra handoffs (not Slack-to-Slack handoffs)
- post status updates to Slack until terminal completion
- preserve output continuity across ownership transfer

## V1 scope (parity-first)

- core employees: alex, tim, ross, garth, joanne, anna
- current production skill surface only
- current Slack output forms preserved
- existing channel knowledge digest behavior preserved

## Contract source of truth

`shared-contracts` is canonical for all execution envelopes and module schemas.

## Tool source of truth

`skill-factory/tools/v1` is the reusable Cogito tool-spec layer. `agent-factory` loads those specs at startup:

- `SHARED_CONTRACTS_DIR`
- `SKILL_FACTORY_DIR`
- `SKILL_TOOL_SPECS_DIR`

## Inference and research provider

This implementation is Gemini-only:

- `INFERENCE_PROVIDER=gemini`
- `GEMINI_MODEL` (default `gemini-2.5-pro`)
- `GEMINI_API_KEY` (default platform key)
- optional `BYOK_GEMINI_API_KEY` override

OpenRouter is intentionally not used in this stack.

## Environment model

Keep your existing `.env.dev` and `.env.prod` workflow, but use examples as the contract:

- copy `.env.dev.example` -> `.env.dev`
- copy `.env.prod.example` -> `.env.prod`

Both include:

- orchestrator app credentials (`ORCHESTRATOR_SLACK_*`)
- per-employee credentials (`<EMPLOYEE>_SLACK_*` for each running agent, e.g. `JOANNE_SLACK_*`, `ROSS_SLACK_*`, `ALEX_SLACK_*`, …)
- shared runtime plumbing (`NATS_URL`, `REDIS_URL`, `BACKEND_INTERNAL_SERVICE_TOKEN`)
- Gemini provider defaults (`GEMINI_*`)

## Core local stack (five repos)

`docker-compose.core.yml` wires:

- `slack-orchestrator` (ingress + dispatch only, Tier-1 tool intent disabled)
- `agent-factory-admin` (runtime authority for catalog + admin data paths)
- `agent-factory` employees (`agent-joanne`, `agent-ross`, `agent-alex`, `agent-garth`, `agent-tim`, `agent-anna`)
- `makeacompany-backend` + `makeacompany-frontend`
- `skill-factory` validator service
- `shared-contracts` validator service
- local `nats` + `redis`
- **Cold-start mirrors of prod CronJobs (profile `local` only):**
  - `makeacompany-slack-snapshots` — loops `POST /v1/internal/refresh-slack-users-snapshot` and `…/refresh-slack-member-channels-snapshot` against the compose backend (same bearer as `BACKEND_INTERNAL_SERVICE_TOKEN`).
  - `channel-knowledge-refresh` — runs `/app/channel-knowledge-refresh` from the **`geeemoney/employee-factory`** image on an interval, using **`ORCHESTRATOR_SLACK_BOT_TOKEN`** as `SLACK_BOT_TOKEN` so history scrape matches prod (orchestrator must be **in** every company channel you expect digests for). The service is pinned to **`platform: linux/amd64`** because shipped tags are amd64-only (Apple Silicon otherwise errors with *no matching manifest for linux/arm64*). Override image with **`CHANNEL_KNOWLEDGE_REFRESH_IMAGE`** if your tag must match a build that writes **`agent-factory:*`** digest keys.

Run:

- `docker compose -f docker-compose.core.yml --env-file .env.dev --profile local up --build`

This boots the full local MakeACompany + agent runtime loop and keeps Slack-derived Redis snapshots and channel knowledge in motion like a cold-started cluster.

Serve mode now consumes orchestrator envelopes from JetStream:

- subject: `slack.work.<employee>.events`
- stream: `SLACK_WORK` (configurable via `NATS_STREAM`)
- employee binding: `EMPLOYEE_ID`

## Memory-bank v1 (structured warm-up)

`agent-factory` can load structured memory from `MEMORY_BANK_FILE` (default:
`/workspace/shared-contracts/memory-bank.v1.json`).

Current behavior:

- conversation turns (`decision.kind=conversation`, no `tool_id`) use memory-bank context + latest human message
- latest human message remains primary; memory is supporting context
- employee intent/expertise/challenge style and channel/thread summaries are injected into fallback prompts
- task turns remain skill-first and can reuse the same memory-bank source as we expand tool execution context

## Key runtime invariant

`mention_ownership_with_internal_delegation`:

- explicit @mention sets ingress owner
- if skill missing, owner delegates internally and posts "transferring to..." update
- users do not manually reroute
- final output remains tied to original thread/trace

## Rancher prod secret sync

Use the canonical sync entrypoint in this repo to mirror one prod keyset into all runtime namespaces:

- `./scripts/update-rancher-secrets.sh`
- defaults:
  - `ENV_FILE=.env.prod`
  - `KUBECONFIG=~/.kube/config/admin.yaml`
  - `KUBE_CONTEXT=admin`
  - `TARGET_SECRET_MAP=agent-factory:agent-factory-runtime,slack-orchestrator:slack-orchestrator-runtime,makeacompany-ai:makeacompany-ai-runtime-secrets`

Options:

- `TARGET_SECRET_MAP` to override namespace/secret targets
- `ROLLOUT_AFTER_SECRET_SYNC=true` to restart workloads after secret apply
- `PULL_SECRET_SOURCE_NAMESPACE` / `PULL_SECRET_FALLBACK_NAMESPACE` to control dockerhub-pull copy source

## Prod cutover readiness signal

Before deprecating `employee-factory`, require all of the following:

1. `agent-factory` namespace exists with healthy admin + employee pods.
2. Mirrored runtime secrets exist in `agent-factory`, `slack-orchestrator`, and `makeacompany-ai`.
3. Tag-driven release workflows pass in all repos:
   - `agent-factory`
   - `skill-factory`
   - `shared-contracts`
   - `slack-orchestrator`
   - `makeacompany-ai`
4. `RANCHER_ADMIN_REPO_TOKEN` exists in every repo that performs GitOps manifest writes.
5. Slack round-trip smoke test passes on `agent-factory` employees for core paths.
