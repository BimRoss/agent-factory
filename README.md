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

## Core local stack (four repos)

`docker-compose.core.yml` wires:

- `slack-orchestrator` (ingress + dispatch only, Tier-1 tool intent disabled)
- `agent-factory` employees (`agent-joanne`, `agent-ross`, `agent-alex`, `agent-garth`, `agent-tim`, `agent-anna`)
- `skill-factory` validator service
- `shared-contracts` validator service
- local `nats` dependency

Run:

- `docker compose -f docker-compose.core.yml --env-file .env.dev --profile local up --build`

This leaves `makeacompany-ai` untouched while you test the core infra loop.

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
