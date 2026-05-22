# Contributing

## Authoring commands

- Files: `command.<name>.go` where `<name>` is the top-level subcommand (`command.api.go` covers all `api` subcommands); split only when a group grows large.
- Public (`cmd/`): a `Command` struct plus a flags struct embedding `CommandFlags`. Declare flags via struct tags (`flag`, `short`, `desc`, `default`, `enum`, `required`).
- Every leaf (no `Children`) must declare an `Output` (`*CommandOutput[T]`) with `TextDescription`, at least one `Examples` entry, and a `JQExample` that includes `--jq`. Use `JSONAny` for free-shape JSON, `JSONUndefined` for raw passthrough; set `JSONArrayStreamed: true` for commands that emit a stream of records.
- Use `ctx.Output*` for stdout, `ctx.Log*` (or `VerboseLog*` behind `--verbose`) for stderr; never mix the two. `--output` controls stdout only.
- Return typed errors (`cmd.NewErrUsage`/`NewErrAuth`/...) so the framework maps them to the right exit code. New exit codes go in `Command.Errors` via `ErrorDescOf[*ErrFoo]()`.
- Before prompting, check `ctx.IsInteractive()` and fail fast otherwise: the CLI never prompts in non-TTY contexts.
- Internal (`internal/cmd/`): runner registered in `init()` via `Register("parent child", runner)`; path and flag type must match.
- Parents (commands with subcommands) are not executable: no run function, no `Flags`.
- Avoid shorthand flags and positional args unless really needed.
- Enum values are `lowercase-kebab-case`.
- Tests: `command.<name>_test.go` (package `cmd_test`); name `Test_ParentCmd_SubCmd_WhatThisTests` (e.g. `Test_API_Management_DefaultGET`).

## End-to-end tests

E2e tests live in `internal/e2e-tests/` behind the `e2e` build tag and run against a live Baseten environment. They auto-skip when `BASETEN_E2E_TEST_API_KEY` is unset, so CI must opt in explicitly.

- Keep them fast and high-level smoke only; do not exhaustively cover flag permutations (that belongs in unit tests).
- Invoke the CLI in-process via `cmd.Execute` rather than shelling out to a built binary.
- Tests must be idempotent and clean up resources they create.
- Use random/unique identifiers for created resources so parallel or repeated runs don't clash.
- Do not assume specific orgs, models, or other state exists; create or look up dynamically.

```bash
BASETEN_E2E_TEST_API_KEY=... \
BASETEN_E2E_TEST_REMOTE_URL=... \
    go test -tags=e2e ./internal/e2e-tests/...
```
