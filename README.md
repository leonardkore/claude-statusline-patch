# claude-statusline-patch

`claude-statusline-patch` is a narrow CLI for patching Claude Code's statusline refresh path to a fixed interval.

Phase 1 scope is intentionally small:

- live-verified target: Linux `x86_64` + Claude Code `2.1.84`, `2.1.85`, `2.1.86`, `2.1.87`, `2.1.89`, `2.1.90`, `2.1.91`, `2.1.92`, `2.1.94`, `2.1.97`, `2.1.100`
- public commands: `ensure`, `apply`, `check`, `restore`, `version`
- default interval: `1000ms`
- transactional binary replacement with tool-owned backup state
- `apply --dry-run` preflight uses the real apply/rebuild path without mutating the binary

Phase 1 does **not** include:

- `tweakcc` integration
- Claude hook logic
- prompt/model/theme patching
- auto-update logic
- multi-version heuristics or a general-purpose patch framework

Other OS binaries may be built for release distribution, but they are not claimed as live-verified unless explicitly tested and documented.

## Install

Latest tagged release:

```bash
go install github.com/leonardkore/claude-statusline-patch@latest
```

If you need an exact pinned release, use:

```bash
go install github.com/leonardkore/claude-statusline-patch@vX.Y.Z
```

## Usage

Normal path after a Claude update:

```bash
claude-statusline-patch ensure
```

`ensure` is the default operator path. It resolves the active Claude binary, acquires a per-binary lock, classifies the current state, applies the patch only when needed, runs live verification using the existing 8-sample local verifier semantics on Linux `x86_64`, and prints `DONE` only after verified success.

`ensure` exit codes:

- `0` verified success
- `1` patch update required
- `2` verification inconclusive or unavailable
- `3` operator intervention required
- `4` local error

Low-level inspection path:

```bash
claude-statusline-patch check
```

`check` exit codes:

- `0` patched
- `1` unpatched
- `2` unrecognized statusline shape
- `3` operational error
- `4` ambiguous or structurally inconsistent patch state

Apply the fixed interval patch:

```bash
claude-statusline-patch apply --interval-ms 1000
```

Preflight the same apply path without writing:

```bash
claude-statusline-patch apply --dry-run --interval-ms 1000
```

Restore the original binary from this tool's managed backup:

```bash
claude-statusline-patch restore
```

Print the tool version:

```bash
claude-statusline-patch version
```

All commands also accept:

```bash
--binary /path/to/claude
```

`ensure`, `apply`, and `restore` also accept:

```bash
--interval-ms 1000
```

By default the CLI resolves `~/.local/bin/claude`, follows symlinks to the canonical installed binary, and patches any uniquely recognized statusline shape family that passes rebuild validation.

`ensure` reports:

- `ensure_outcome` as one of:
  - `verified_success`
  - `patch_update_required`
  - `verification_inconclusive_or_unavailable`
  - `operator_intervention_required`
  - `local_error`
- `ensure_action` when the run completed successfully by:
  - verifying an existing managed patch
  - applying and verifying during the current run
- `verified_tuple_match: true` only when the current installed bytes, interval, platform tuple, and verifier-contract version exactly match a previously verified tuple; `ensure` still runs live verification before printing `DONE`
- `DONE` only when the active binary is verified safe for the requested interval

`check` reports:

- `shape_id` and `observed_versions` when the current binary matches a known family
- `support_claim` as one of:
  - `live_verified`
  - `patchable_only`
  - `undocumented`
- `verification_claim` as a legacy compatibility alias:
  - `live-verified`
  - `not-live-verified`
- `quick_apply_candidate: true` when the binary is in a uniquely known unpatched shape

## Backup State

This tool stores its own persistent backup state under:

```bash
$XDG_STATE_HOME/claude-statusline-patch
```

When `XDG_STATE_HOME` is set, this tool requires it to be an absolute path inside the current user's home directory.

If `XDG_STATE_HOME` is unset, the default is:

```bash
~/.local/state/claude-statusline-patch
```

Backups are keyed by the canonical target path plus the original SHA-256 so multiple installs of the same Claude version do not collide.

## Verification Boundary

Phase 1 support claims are intentionally strict:

- verified Claude versions: `2.1.84`, `2.1.85`, `2.1.86`, `2.1.87`, `2.1.89`, `2.1.90`, `2.1.91`, `2.1.92`, `2.1.94`, `2.1.97`, `2.1.100`
- verified OS: Linux
- verified architecture: `x86_64`

## Compatibility Table

| Platform | Claude version | Shape family | Structurally patchable | Live-verified | First supporting release | Notes |
| --- | --- | --- | --- | --- | --- | --- |
| Linux `x86_64` | `2.1.84` | `statusline_debounce_v1` | yes | yes | `v0.1.2` | real unpatched fixture and generated patched fixture tracked |
| Linux `x86_64` | `2.1.85` | `statusline_debounce_v1` | yes | yes | `v0.1.2` | real unpatched fixture and generated patched fixture tracked |
| Linux `x86_64` | `2.1.86` | `statusline_debounce_v2` | yes | yes | `v0.2.2` | real unpatched fixture and generated patched fixture tracked; version detection narrowed to the Claude metadata block |
| Linux `x86_64` | `2.1.87` | `statusline_debounce_v2` | yes | yes | `v0.2.3` | live-verified quick-apply candidate; authoritative unpatched fixture and generated patched fixture tracked |
| Linux `x86_64` | `2.1.89` | `statusline_debounce_v2` | yes | yes | `v0.2.4` | live-verified quick-apply candidate; authoritative unpatched fixture and generated patched fixture tracked |
| Linux `x86_64` | `2.1.90` | `statusline_debounce_v2` | yes | yes | `v0.2.4` | live-verified quick-apply candidate; authoritative unpatched fixture and generated patched fixture tracked |
| Linux `x86_64` | `2.1.91` | `statusline_debounce_v2` | yes | yes | `v0.2.5` | live-verified quick-apply candidate; authoritative unpatched fixture and generated patched fixture tracked |
| Linux `x86_64` | `2.1.92` | `statusline_debounce_v2` | yes | yes | `v0.2.6` | live-verified quick-apply candidate; authoritative unpatched fixture and generated patched fixture tracked |
| Linux `x86_64` | `2.1.94` | `statusline_debounce_v2` | yes | yes | `v0.2.7` | live-verified quick-apply candidate; authoritative unpatched fixture and generated patched fixture tracked |
| Linux `x86_64` | `2.1.97` | `statusline_debounce_v2` | yes | yes | `v0.2.8` | live-verified quick-apply candidate; authoritative unpatched fixture and generated patched fixture tracked |
| Linux `x86_64` | `2.1.100` | `statusline_debounce_v2` | yes | yes | `v0.2.9` | live-verified quick-apply candidate; authoritative unpatched fixture and generated patched fixture tracked |
| Linux `x86_64` | `2.1.119` | `statusline_debounce_v3` | yes | no | unreleased | structurally patchable through `ensure`; local patched verification passed, but strict restored-baseline proof is not complete |
| Linux `x86_64` | future version with known family | `statusline_debounce_v1` or later | maybe | no, until live-verified | UNKNOWN | use `check` then `apply --dry-run` before changing code |

See [docs/verification.md](docs/verification.md) for the exact local verification sequence and [docs/releasing.md](docs/releasing.md) for release rules and asset expectations.
