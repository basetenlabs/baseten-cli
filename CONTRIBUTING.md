# Contributing

## Authoring commands

- Files: `command.<name>.go` where `<name>` is the top-level subcommand (`command.api.go` covers all `api` subcommands); split only when a group grows large.
- Public (`cmd/`): a `Command` struct plus a flags struct embedding `CommandFlags`. Declare flags via struct tags (`flag`, `short`, `desc`, `default`, `enum`, `required`).
- Every leaf (no `Children`) must declare an `Output` (`*CommandOutput[T]`) with `TextDescription`, at least one `Examples` entry, and a `JQExample` that includes `--jq`. Use `JSONAny` for free-shape JSON, `JSONUndefined` for raw passthrough; set `JSONArrayStreamed: true` for commands that emit a stream of records.
- An example that does not fit help's width is rejected at build time. Split it into `CommandLines` (instead of `Command`) at argument boundaries, never inside a quoted argument; help adds the trailing `\` and the indent.
- Use `ctx.Output*` for stdout, `ctx.Log*` (or `VerboseLog*` behind `--verbose`) for stderr; never mix the two. `--output` controls stdout only.
- Return typed errors (`cmd.NewErrUsage`/`NewErrAuth`/...) so the framework maps them to the right exit code. New exit codes go in `Command.Errors` via `ErrorDescOf[*ErrFoo]()`.
- Under `--output json`/`jsonl` the framework writes a `cmd.JSONErrorEnvelope` to stdout for a failed command, in addition to the stderr message. Call `ctx.SuppressJSONError()` before returning if the payload already written reports the failure itself.
- Before prompting, check `ctx.IsInteractive()` and fail fast otherwise: the CLI never prompts in non-TTY contexts.
- Internal (`internal/cmd/`): runner registered in `init()` via `Register("parent child", runner)`; path and flag type must match.
- Parents (commands with subcommands) are not executable: no run function, no `Flags`.
- Avoid shorthand flags and positional args unless really needed.
  - `volume` is the exception: it addresses a file tree, so refs are positional and shorthands follow the unix namesakes.
- Enum values are `lowercase-kebab-case`.
- Tests: `command.<name>_test.go` (package `cmd_test`); name `Test_ParentCmd_SubCmd_WhatThisTests` (e.g. `Test_API_Management_DefaultGET`).
- JSONL preserves JSON's response structure without indentation; only explicitly streamed arrays (`JSONArrayStreamed: true`, `NewJSONArrayWriter`) emit one record per line.
- Wrap non-`omitempty` slices the CLI builds for JSON output (such as a list's `Items`) in `normalizeJSONSlice`, so an empty result prints `[]`, not `null`.

## End-to-end tests

E2e tests live in `internal/e2e-tests/` behind the `e2e` build tag and run against a live Baseten environment. They auto-skip when `BASETEN_E2E_TEST_API_KEY` is unset, so CI must opt in explicitly.

- Keep them fast and high-level smoke only; do not exhaustively cover flag permutations (that belongs in unit tests).
- Invoke the CLI in-process via `cmd.Execute` rather than shelling out to a built binary.
- Tests must be idempotent and clean up resources they create.
- Use random/unique identifiers for created resources so parallel or repeated runs don't clash.
- Do not assume specific orgs, models, or other state exists; create or look up dynamically.
- Mark phase boundaries of a long test with `step`, and run with `-v` (as CI does) so they stream and a stuck run is attributable.

```bash
BASETEN_E2E_TEST_API_KEY=... \
BASETEN_E2E_TEST_REMOTE_URL=... \
    go test -v -tags=e2e ./internal/e2e-tests/...
```

## Releasing

Releases are created and published in GitHub, which creates the tag. Never push a release tag directly. Publishing runs the [release workflow](.github/workflows/release.yml) in the background, which fails unless the tag, the pre-release checkbox, and the title agree. It then adds the binaries to the release a few minutes later and updates the [Homebrew tap](https://github.com/basetenlabs/homebrew-baseten).

#### Stable releases

Only repository admins can publish stable releases.

1. Create a GitHub release with a new `vX.Y.Z` tag on `main`, titled `vX.Y.Z`, with release notes in the format below, and publish it.
1. Merge the README PR the workflow opens. Until the README links the new release, CI fails on every PR.

Both the `baseten` and `baseten-rc` Homebrew formulas move to the new release.

Release notes start with hand-written highlights, followed by GitHub's "Generate release notes" output (What's Changed and Full Changelog). Delete the generated New Contributors section entirely.

```markdown
Download binaries below or see [README](https://github.com/basetenlabs/baseten-cli) for install

## Highlights

#### New commands

- `baseten <command>` to <what it does>.

#### Command updates

- `baseten <command>` <what changed>.

## What's Changed
...
```

Highlights describe user-visible changes per command, briefly. Leave out internal changes such as tests and CI, and omit an empty subsection. Notable changes that deserve more than a bullet, such as a new feature area or a breaking change, can get their own `####` subsection above New commands.

#### Release candidates

Anyone with write access can publish a release candidate for teams that need a build before the next stable release.

1. Create a GitHub release with a new `vX.Y.Z-rc.N` tag on any branch, marked as a pre-release, titled starting with `[UNSTABLE] vX.Y.Z-rc.N`, and publish it.

Release candidate notes have no required format, but must state the purpose of the candidate, such as which team needs it and for what.

Only the `baseten-rc` Homebrew formula moves (`brew install baseten-rc`, which conflicts with `baseten`). Repository admins may contact RC builders and confirm RCs can be removed once they are no longer needed.
