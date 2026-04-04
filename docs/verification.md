# Verification

Phase 1 only claims live verification for:

- Linux
- `x86_64`
- Claude Code `2.1.84`
- Claude Code `2.1.85`
- Claude Code `2.1.86`
- Claude Code `2.1.87`
- Claude Code `2.1.89`
- Claude Code `2.1.90`
- Claude Code `2.1.91`
- Claude Code `2.1.92`

Other OS binaries may be built, but they are not claimed as verified unless they were actually tested.

Phase 1 does **not** include:

- `tweakcc` integration
- hook logic
- auto-update logic

## Canonical Verification Sequence

Every new Claude version starts as a quick-apply candidate:

- run `check` on the real binary before editing code
- run `apply --dry-run` before any write if `check` reports a known shape
- if extraction succeeds and one known `shape_id` is reported, treat it as a matcher-layer update, not a container-layer break
- do not widen README or release claims until the full live verification sequence passes

## New Version Update Playbook

Run these steps in order for every newly seen Claude version:

1. `claude-statusline-patch check --binary <path-to-new-version>`
2. If container parsing fails, investigate the binary/container layer before touching matcher code.
3. If the container parses but the shape is unrecognized, extract a real snippet first and compare it to the existing family corpus:

   ```bash
   go run ./tools/extract-statusline-fixture --binary <path-to-new-version> > /tmp/statusline-snippet.js
   ```

4. Add or update fixture entries and the provenance manifest before changing matcher logic.
5. Run `claude-statusline-patch apply --dry-run --binary <path-to-new-version> --interval-ms 1000`
6. If the dry run succeeds, run `apply`
7. Live verify `on`
8. Run `restore`
9. Live verify `off`
10. Update the compatibility table and release notes only after the live sequence succeeds

When verifying the active default install:

- `claude-statusline-switch` may report `state: unknown` if it does not have stored snapshots for that Claude version
- use `claude-statusline-verify off 8` as the actual baseline proof instead of relying on switch metadata alone
- if `claude-statusline-switch off` causes `check --binary <path-to-new-version>` to report a different Claude version than the binary path under test, restore the live binary before version-specific `apply` verification

Start from a clean local baseline:

```bash
claude-statusline-switch off
claude-statusline-verify off 8
```

Expected baseline result:

- `distinct_session_seconds: [0]`

Apply the tool:

```bash
go run ./cmd/claude-statusline-patch apply --dry-run --interval-ms 1000
go run ./cmd/claude-statusline-patch apply --interval-ms 1000
claude-statusline-verify on 8
```

Expected dry-run result:

- the output begins with the current inspection fields for the target binary
- `dry_run: ok`
- `dry_run_rebuild_validation: passed`
- `simulated_state: patched`
- `simulated_interval_ms: 1000`
- `would_apply_interval_ms: 1000`

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
- Claude Code `2.1.86`
  - baseline `off -> [0]`
  - patched `on -> [0,1,2,3,4,5,6]`
  - restored `off -> [0]`
- Claude Code `2.1.87`
  - baseline `off -> [0]`
  - patched `on -> [0,1,2,3,4,5,6]`
  - restored `off -> [0]`
- Claude Code `2.1.89`
  - baseline `off -> [0]`
  - patched `on -> [0,1,2,3,4,5,6]`
  - restored `off -> [0]`
- Claude Code `2.1.90`
  - baseline `off -> [0]`
  - patched `on -> [0,1,2,3,4,5,6]`
  - restored `off -> [0]`
- Claude Code `2.1.91`
  - baseline `off -> [0]`
  - patched `on -> [0,1,2,3,4,5,6]`
  - restored `off -> [0]`
- Claude Code `2.1.92`
  - baseline `off -> [0]`
  - patched `on -> [0,1,2,3,4,5,6]`
  - restored `off -> [0]`

## Important Boundary

Do not use `claude-statusline-switch on` as proof of this tool's behavior during product verification. That switch restores the local operator snapshot, not a binary produced by `claude-statusline-patch`.
