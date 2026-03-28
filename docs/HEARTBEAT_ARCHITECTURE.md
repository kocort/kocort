# Heartbeat Architecture

This document captures the four-layer heartbeat design used in `openclaw`, and
maps it into the kocort domain so we can evolve the current implementation
incrementally without pushing more logic into `runtime/`.

## Goals

- Keep heartbeat behavior autonomous and domain-driven.
- Separate scheduling, wake/coalescing, turn planning, and delivery concerns.
- Keep `runtime` focused on orchestration and dependency wiring.
- Preserve room for future parity features such as quiet hours, duplicate
  suppression, isolated heartbeat sessions, and visibility policies.

## The Four Layers

### 1. Scheduler Layer

Responsibility:

- Decide **when** an agent heartbeat is due.
- Track per-agent interval state.
- Trigger heartbeat wake requests instead of executing runs directly.

OpenClaw reference:

- `src/infra/heartbeat-runner.ts`

Core behavior:

- Maintains per-agent `intervalMs`, `lastRunMs`, `nextDueMs`.
- Schedules the next due wake precisely instead of using a coarse global scan.
- Recomputes schedules when config changes.

Current kocort state:

- `internal/heartbeat/runner.go`
- Maintains per-agent interval state and next due timestamps.
- Schedules the next wake with a single timer instead of one-minute polling.
- Keeps disk-budget maintenance adjacent but separate from turn planning.

### 2. Wake / Coalescing Layer

Responsibility:

- Accept heartbeat wake requests from multiple producers.
- Coalesce duplicate wake requests.
- Serialize heartbeat execution.
- Retry when the runtime is busy.

OpenClaw reference:

- `src/infra/heartbeat-wake.ts`

Core behavior:

- Uses a wake queue keyed by `agentId::sessionKey`.
- Coalesces requests with a default 250 ms delay.
- Preserves higher-priority reasons over lower-priority ones.
- Retries if the main lane is busy.

Current kocort state:

- `internal/heartbeat/wake.go`
- Coalesces by `agentID::sessionKey`.
- Preserves higher-priority wake reasons.
- Retries automatically when the heartbeat runner reports
  `requests-in-flight`.

### 3. Turn Planning Layer

Responsibility:

- Decide **whether** a heartbeat run should happen.
- Build the heartbeat prompt.
- Convert pending system events into internal model-visible events.
- Decide whether the heartbeat may deliver back to the last user target.
- Normalize heartbeat-specific config such as ack limits and model override.

OpenClaw reference:

- `src/infra/heartbeat-runner.ts`
- `src/auto-reply/heartbeat.ts`
- `src/infra/heartbeat-events-filter.ts`

Core behavior:

- Evaluates skip conditions such as disabled heartbeat, quiet hours,
  requests-in-flight, and empty `HEARTBEAT.md`.
- Builds specialized prompts for cron events and exec-completion events.
- Injects workspace path hints to avoid reading the wrong heartbeat file.
- Uses heartbeat-specific model and reply normalization settings.

Target shape in kocort:

- All heartbeat turn-planning rules should live under `internal/heartbeat`.
- Runtime should supply dependencies such as identity, session context,
  system events, and workspace file contents.
- Runtime should not own prompt selection, event filtering, or skip rules.

Implemented in this iteration:

- `internal/heartbeat/content.go`
- `internal/heartbeat/turn.go`

These now own:

- effective-empty `HEARTBEAT.md` detection
- active-hours gating
- heartbeat prompt resolution
- cron / exec event prompt switching
- internal event materialization
- delivery decision
- heartbeat model override propagation
- isolated heartbeat run-session selection

### 4. Delivery / Result Normalization Layer

Responsibility:

- Normalize the heartbeat reply before it is shown or persisted.
- Suppress pure `HEARTBEAT_OK` acknowledgements.
- Keep zero-information heartbeat turns from polluting user-visible output.

OpenClaw reference:

- `src/auto-reply/heartbeat.ts`
- `src/infra/heartbeat-runner.ts`
- `src/infra/heartbeat-events.ts`
- `src/infra/heartbeat-visibility.ts`

Core behavior:

- strips `HEARTBEAT_OK`
- restores session freshness when a heartbeat was only an ack
- prunes transcript growth for no-op heartbeats
- emits heartbeat UI/telemetry events
- suppresses duplicate heartbeat alerts

Current kocort state:

- `internal/heartbeat/heartbeat.go`
- `runtime/runtime_heartbeat.go`
- `internal/delivery/transcript.go`

What kocort already does:

- strips `HEARTBEAT_OK`
- suppresses ack-only user delivery
- strips heartbeat ack content again during transcript persistence
- restores session freshness for ack-only and duplicate heartbeat turns
- suppresses duplicate heartbeat text within a short delivery window
- prunes transcript entries for ack-only and duplicate heartbeat runs
- skips heartbeat execution while the main runtime lane already has in-flight work
- supports isolated heartbeat run sessions with fresh transcript IDs
- respects heartbeat active-hours in the configured timezone
- resolves heartbeat visibility (`showOk/showAlerts/useIndicator`)
- emits heartbeat status events for UI/telemetry consumers
- keeps system events queued when a heartbeat is skipped before execution
- sends `HEARTBEAT_OK` only when visibility allows it
- supports explicit heartbeat routing fields such as `session`, `to`, and `accountId`

What is still missing:

- richer channel readiness checks before heartbeat delivery
- full OpenClaw-equivalent delivery target resolution (allowFrom/account validation/direct-chat heuristics)
- snapshot-based transcript rollback instead of run-id pruning only
- richer quiet-hours-style policy beyond active-hours gating

## Current Domain Boundaries In Kocort

The intended ownership after this refactor is:

- `internal/heartbeat`
  - scheduler state
  - wake bus and future retry semantics
  - heartbeat file evaluation
  - event filtering
  - run planning
  - reply normalization helpers
- `runtime`
  - dependency assembly
  - identity/session resolution
  - execution of `Runtime.Run(...)`
  - forwarding normalized output to the configured deliverer

## Incremental Roadmap

### Phase 1

- Add design document.
- Move turn planning logic from `runtime/runtime_heartbeat.go` into
  `internal/heartbeat`.
- Make `heartbeat.model` effective for heartbeat runs.
- Treat comment-only / placeholder-only `HEARTBEAT.md` as empty.

### Phase 2

- Add wake reason priority and busy-lane retry to `internal/heartbeat/wake.go`.
- Introduce richer heartbeat reason classification in the domain layer.

### Phase 3

- Add duplicate heartbeat suppression.
- Restore session freshness for ack-only and duplicate heartbeat turns.
- Add transcript pruning for ack-only turns.
- Skip heartbeat execution while the target session is already busy.
- Add optional visibility/indicator hooks.

### Phase 4

- Add isolated heartbeat session mode.
- Add quiet hours / active-hours policy in domain configuration.
- Replace coarse one-minute scheduler scan with precise next-due scheduling.

Status:

- isolated heartbeat session mode: implemented
- active-hours policy: implemented
- precise next-due scheduling: implemented
