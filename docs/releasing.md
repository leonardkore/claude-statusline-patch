# Releasing

Phase 1 release policy is strict:

- live-verified target: Linux `x86_64` + Claude Code `2.1.84`, `2.1.85`, `2.1.86`, `2.1.87`, `2.1.89`, `2.1.90`
- other OS binaries may be built and attached, but they must not be described as live-verified unless they were actually tested
- no `tweakcc` integration, no hook logic, no auto-update logic
- support for a new Claude version is a patch release unless the CLI contract changes

## Pre-merge

Before opening a PR:

- `go test ./...`
- `gofmt -l .`
- `go vet ./...`
- live verification captured for:
  - `check`
  - `apply --dry-run --interval-ms 1000`
  - baseline `off`
  - `apply --interval-ms 1000`
  - idempotent re-apply
  - `restore`

The PR must record:

- Claude version tested: exact tested version(s), for example `2.1.84`, `2.1.85`, `2.1.86`, `2.1.87`, `2.1.89`, `2.1.90`
- OS tested: Linux
- architecture tested: `x86_64`
- external patchers inactive during verification: yes
- live results:
  - `off -> [0]`
  - `apply + verify -> [0,1,2,3,4,...]`
  - `restore -> [0]`

## Merge and Tagging

Phase 1 release flow:

1. merge the feature branch into `main`
2. update `main`
3. create an annotated tag
4. push the tag
5. let the release workflow build and publish the GitHub Release

Commands:

```bash
git checkout main
git pull --ff-only
git tag -a <next-release-tag-vX.Y.Z> -m "<next-release-tag-vX.Y.Z>"
git push origin <next-release-tag-vX.Y.Z>
```

Example:

```bash
git tag -a v0.2.5 -m "v0.2.5"
git push origin v0.2.5
```

Do not tag from the feature branch.
Do not create a release before merge.

## Release Metadata

Release title:

```text
<next-release-tag-vX.Y.Z>
```

Release notes must include:

- supported Claude versions verified: `2.1.84`, `2.1.85`, `2.1.86`, `2.1.87`, `2.1.89`, `2.1.90`
- supported OS live-verified: Linux
- binaries attached for each target in the build matrix
- `SHA256SUMS.txt`
- install instructions:

```bash
go install github.com/leonardkore/claude-statusline-patch@<next-release-tag-vX.Y.Z>
```

- warning that only Linux + Claude `2.1.84`, `2.1.85`, `2.1.86`, `2.1.87`, `2.1.89`, `2.1.90` are live-verified unless more testing was actually done
