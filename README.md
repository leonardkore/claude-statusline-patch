# claude-statusline-patch

`claude-statusline-patch` is a narrow CLI for patching Claude Code's statusline refresh path to a fixed interval.

Phase 1 scope is intentionally small:

- live-verified target: Linux `x86_64` + Claude Code `2.1.84`, `2.1.85`
- public commands: `apply`, `check`, `restore`, `version`
- default interval: `1000ms`
- transactional binary replacement with tool-owned backup state

Phase 1 does **not** include:

- `tweakcc` integration
- Claude hook logic
- prompt/model/theme patching
- auto-update logic
- multi-version heuristics or a general-purpose patch framework

Other OS binaries may be built for release distribution, but they are not claimed as live-verified unless explicitly tested and documented.

## Install

Tagged-release install path:

```bash
go install github.com/leonardkore/claude-statusline-patch@v0.1.1
```

## Usage

Check the current binary state:

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

By default the CLI resolves `~/.local/bin/claude`, follows symlinks to the canonical installed binary, and patches any uniquely recognized statusline shape family that passes rebuild validation.

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

- verified Claude versions: `2.1.84`, `2.1.85`
- verified OS: Linux
- verified architecture: `x86_64`

See [docs/verification.md](docs/verification.md) for the exact local verification sequence and [docs/releasing.md](docs/releasing.md) for release rules and asset expectations.
