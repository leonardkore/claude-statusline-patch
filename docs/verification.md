# Verification

Phase 1 only claims live verification for:

- Linux
- `x86_64`
- Claude Code `2.1.84`
- Claude Code `2.1.85`

Other OS binaries may be built, but they are not claimed as verified unless they were actually tested.

Phase 1 does **not** include:

- `tweakcc` integration
- hook logic
- auto-update logic

## Canonical Verification Sequence

Every new Claude version starts as a quick-apply candidate:

- run `check` on the real binary before editing code
- if extraction succeeds and one known `shape_id` is reported, treat it as a matcher-layer update, not a container-layer break
- do not widen README or release claims until the full live verification sequence passes

When verifying the active default install:

- `claude-statusline-switch` may report `state: unknown` if it does not have stored snapshots for that Claude version
- use `claude-statusline-verify off 8` as the actual baseline proof instead of relying on switch metadata alone

Start from a clean local baseline:

```bash
claude-statusline-switch off
claude-statusline-verify off 8
```

Expected baseline result:

- `distinct_session_seconds: [0]`

Apply the tool:

```bash
go run ./cmd/claude-statusline-patch apply --interval-ms 1000
claude-statusline-verify on 8
```

Expected patched result:

- the verifier observes at least `0,1,2,3,4`

Test idempotency:

```bash
go run ./cmd/claude-statusline-patch apply --interval-ms 1000
```

Expected idempotency result:

- no-op or explicit `already patched`

Restore:

```bash
go run ./cmd/claude-statusline-patch restore
claude-statusline-verify off 8
```

Expected restored result:

- `distinct_session_seconds: [0]`

Observed live-verified results on Linux `x86_64`:

- Claude Code `2.1.84`
  - baseline `off -> [0]`
  - patched `on -> [0,1,2,3,4,5,6]`
  - restored `off -> [0]`
- Claude Code `2.1.85`
  - baseline `off -> [0]`
  - patched `on -> [0,1,2,3,4,5,6]`
  - restored `off -> [0]`

## Important Boundary

Do not use `claude-statusline-switch on` as proof of this tool's behavior during product verification. That switch restores the local operator snapshot, not a binary produced by `claude-statusline-patch`.
