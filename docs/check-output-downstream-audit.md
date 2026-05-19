# Check Output Downstream Audit

Audit date: 2026-05-19

## Scope

This audit covers known local downstream consumers for `claude-statusline-patch check` output after the CLI began emitting richer status fields such as `shape_id`, `shape_state`, `patch_state`, `support_claim`, and `verification_claim`.

Searched locations:

- `/home/dev/.bashrc`
- `/home/dev/.zshrc`
- `/home/dev/.profile`
- `/home/dev/.config`
- `/home/dev/.claude`
- `/home/dev/.codex`
- `/home/dev/projects`

Excluded generated or non-consumer trees:

- `.git`
- `node_modules`
- Go module cache under `/home/dev/go/pkg/mod`
- package/cache trees under `.cache` and `.local/share`
- this repository when identifying external consumers

Patterns searched:

- `claude-statusline-patch check`
- `state: unsupported`
- `state: ambiguous`
- `unrecognized_shape`
- `ambiguous_shape`
- `shape_state`
- `patch_state`
- `verification_claim`

## Findings

No external local automation was found that parses the old `check` output contract or the old broad state names.

The matches inside this repository are documentation, tests, fixture metadata, and CLI implementation. They already use the current state names and richer output fields.

The only matches outside this repository were Codex session logs and historical command logs. Those are audit/debug artifacts, not executable consumers.

## Compatibility Decision

Known local downstream consumers do not require code changes for the current `check` output contract.

If a future external consumer is discovered, prefer updating that consumer to use the current explicit fields. If multiple consumers need a stable parser boundary, add a machine-readable output mode instead of relying on fixed human-readable line positions.
