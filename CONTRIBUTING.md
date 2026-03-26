# Contributing

## Command Best Practices

- **Commands with subcommands are not themselves executable.** A parent command
  (e.g. `baseten api`) only serves as a grouping for its children. It should not
  have a run function or `Flags` set on its `Command` definition.

- **Do not use shorthand flags unless you really need them.** Single-letter flag
  aliases are strongly discouraged.

- **Do not use positional args unless it really makes sense.** Prefer named flags
  over positional arguments.

## How to Write a Command

- Commands live in files named `command.<name>.go` where `<name>` is the highest-level subcommand (e.g. `command.api.go` for all `api` subcommands). Split into separate files only if a subcommand group is large enough to warrant it.
- **Public side** (`cmd/`): Define a `Command` struct and a flags struct. The flags struct must embed `CommandFlags` (directly or transitively). Use struct tags (`flag`, `short`, `desc`, `default`, `enum`, `required`) to declare flags.
- **Internal side** (`internal/cmd/`): Write the runner function and register it in an `init()` with `Register("parent child", myRunner)`. The path and flag type must match the command definition exactly.
- **Tests** go in `command.<name>_test.go` in the `cmd_test` package. Test names follow `Test_ParentCmd_SubCmd_WhatThisTests` (e.g. `Test_API_Management_DefaultGET`).
