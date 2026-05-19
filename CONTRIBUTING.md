# Contributing

## Authoring commands

- Files: `command.<name>.go` where `<name>` is the top-level subcommand (`command.api.go` covers all `api` subcommands); split only when a group grows large.
- Public (`cmd/`): a `Command` struct plus a flags struct embedding `CommandFlags`. Declare flags via struct tags (`flag`, `short`, `desc`, `default`, `enum`, `required`).
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
