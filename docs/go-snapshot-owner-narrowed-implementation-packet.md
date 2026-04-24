# Go Snapshot Owner Narrowed Implementation Packet

## Decision Summary
- Mode/phase: design / spec-review approval
- Recommendation type: review (narrow)
- Approval status: APPROVE WITH REQUIRED CHANGES
- Scope: implement the approved snapshot-owner architecture with narrowed boundaries closed now

## Problem Statement
Claude refreshes statusline every second and aborts unfinished renders on the next tick. The current per-second renderer still owns slow-path freshness/discovery decisions. The architecture must move those decisions fully off the render path.

## Ground Truth
- Claude is running `2.1.92`; patch state is `patched` with `interval_ms: 1000` (`claude-statusline-patch check`).
- Shared renderer currently has `show_weather=0` and still gates repo core on `show_git || show_health || show_action || show_model`:
  - [statusline-command.sh](/home/dev/.claude/statusline/statusline-command.sh#L235)
  - [statusline-command.sh](/home/dev/.claude/statusline/statusline-command.sh#L248)
- `local_systems.py render-snapshot` remains synchronous in the current hot path:
  - [statusline-command.sh](/home/dev/.claude/statusline/statusline-command.sh#L1316)
  - [statusline-command.sh](/home/dev/.claude/statusline/statusline-command.sh#L2355)
- Current direct render timing with weather paused is around `0.44s` to `0.49s` in this repo and around `0.39s` to `0.45s` in `/home/dev/projects/Codex/kenya_law`.
- Open-Meteo request used by the weather path still times out at `5.0s` in this environment.

## Closed Architecture Decisions
1. Runtime owner shape:
One long-lived local Go agent is required. One-shot mode is rejected.

2. Wrapper role:
Bash remains temporary and only does: read snapshot, emit best-effort non-blocking key heartbeat, merge Claude stdin fields, format output.

3. Hot-path prohibitions:
No network calls, no repo scans, no Python helper execution, no refresh/freshness decisions on the render path.

4. Key model:
Two-level keying is required.
- Render snapshot lookup key: lossless encoding of raw cwd string.
- Internal section/cache key: canonical normalized repo/worktree identity.

5. State ownership:
Go agent is sole writer for:
- weather caches
- local-systems caches
- active-key registry
- composed per-cwd render snapshots

6. Local-systems authority boundary:
No second public authority is allowed.
- During migration, Python may be called only behind the Go agent.
- Steady state requires one canonical identity/parsing/trust contract.

7. Render snapshot semantics:
Composed render snapshots are derived artifacts.
- Write atomically.
- Write on first-seen key or section-state change only.
- Never rewrite every second.

8. Cold-start and failure behavior:
Render path never blocks.
- If last-good exists: serve last-good with freshness/error metadata.
- If missing: section omission/loading, not inline fallback work.
- If agent down: wrapper serves last-good/omission only.

9. Retention and active-key bounds:
- Refresh only recently heartbeating keys.
- Evict stale per-cwd render snapshots by inactivity.
- Prevent unbounded key accumulation.

10. Read-path budgets:
- Wrapper p95 <= 100ms
- Wrapper p99 <= 250ms
- Exceeding these budgets is release-blocking for snapshot-read rollout.

## Required Changes Before Approval Closes
1. Ensure long-lived Go agent is outside render path and wrapper heartbeat registration is non-blocking.
2. Specify and test two-level key behavior, including symlink/worktree collisions and cleanup.
3. Close local-systems authority boundary to one canonical contract called by agent only.
4. Enforce quantitative read-path and write-on-change constraints.
5. Prove crash and cold-start behavior end to end (owner down, missing/corrupt snapshot, multi-pane contention, last-good rollback, live pane validation).

## Rollout Path
1. Start Go agent and wrapper snapshot-read + heartbeat contract with output compatibility.
2. Move weather behind agent and verify zero render-path network work.
3. Move local-systems behind agent and remove synchronous Python from render path.
4. Enable composed per-cwd render snapshots with atomic write-on-change semantics.
5. Measure p95/p99 wrapper latency and multi-pane live behavior; remove old inline paths only after passing.

## Explicit Non-Goals
- Weather-only migration
- Synchronous `local_systems.py` optimization on render path
- Global single render snapshot for multi-pane cwd usage
- Full Go renderer rewrite in this round
- Hosted runtime introduction in this round
