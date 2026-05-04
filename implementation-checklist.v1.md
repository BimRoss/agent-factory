# Implementation Checklist (V1)

## Parity inventory

- [ ] Confirm 6 employees from `shared-contracts/parity-inventory.v1.md`
- [ ] Confirm current skill list and employee assignment map
- [ ] Confirm Slack output classes preserved (blocks, confirmations, fallback text)
- [ ] Confirm channel knowledge digest parity baseline

## Contracts and runtime wiring

- [ ] Consume `employee.contract.v1`
- [ ] Consume `skill.contract.v1`
- [ ] Emit `status.contract.v1` per lifecycle transition
- [ ] Persist handoffs via `handoff.contract.v1`
- [ ] Preserve accumulated outputs via `execution_trace.contract.v1`
- [ ] Render final replies via `output_render.contract.v1`

## Behavioral tests

- [ ] Mentioned owner has skill -> execute + complete
- [ ] Mentioned owner lacks skill -> transfer update + internal handoff + complete
- [ ] Handoff rejection -> fallback path + explicit failure/next-step output
- [ ] Retry policy honored for transient failures
- [ ] Duplicate final-post prevention validated

## Operability

- [ ] Lifecycle transition logs include task/trace/owner metadata
- [ ] Handoff latency measurable
- [ ] Orphaned task sweeper policy implemented
- [ ] P95 time-to-first-status-update measured
