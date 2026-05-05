# MakeACompany Rearchitecture RFC (V1 Bootstrap)

## Why rebuild this layer

MVP proved product value, but execution in the legacy runtime now mixes:

- skill logic
- routing logic
- output rendering logic
- async orchestration glue

This RFC defines a modular replacement where intelligence lives in employee runtimes and skills, not in brittle hardcoded routing branches.

## Architecture target

- `slack-orchestrator`: ingress + dispatch only
- `agent-factory`: employee runtime shell, lifecycle ownership, handoff execution
- `skill-factory`: reusable skill contracts and packaging
- `shared-contracts`: canonical schema/version spine

## Core runtime behavior

1. Slack message enters via orchestrator and reaches initial owner (for example `@joanne`).
2. Owner plans with Cogito and selects an available skill.
3. If required skill is missing, owner performs internal handoff in infra.
4. Owner posts user-facing status update to Slack (for example "Transferring to Ross for issue workflow...").
5. New owner continues execution using same `task_id` and `trace_id`.
6. Final owner posts terminal output using accumulated contract-renderable outputs.

## Async lifecycle states

- `received`
- `planning`
- `running`
- `waiting_handoff`
- `handoff_accepted`
- `finalizing`
- terminal: `completed` | `failed` | `cancelled`

All transitions emit structured status events.

## Mention ownership invariant

`mention_ownership_with_internal_delegation` is mandatory:

- mention target owns ingress
- missing-skill path delegates internally
- Slack sees progress updates, not inter-agent coordination internals
- users never manually reroute work

## V1 boundaries (must hold)

- No new employees beyond current 6
- No net-new production skills
- Phase subset for packaged parity skills excludes `create-email-welcome`
- `read-web` and `read-skills` are runtime-native capabilities (not first-pass packaged skills)
- No non-Slack transport implementation
- Preserve current output forms and channel-knowledge behavior

## Cogito leverage model

Employee runtime is Cogito-first by default:

- one Cogito runtime per employee module
- skill toolchain exposed via contract-defined tools
- supports both markdown-defined skills and Go-backed complex tools
- same planner/executor contract for both skill types

This allows simpler onboarding for new skill creators while preserving an escape hatch for deep integrations.

## Success criteria

- parity feature coverage for existing employees/skills
- lower routing code complexity than the legacy runtime
- consistent, contract-defined Slack output behavior across handoffs
- deterministic final-post idempotency for async tasks
