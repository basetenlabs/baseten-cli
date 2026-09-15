# internal/cmddocs

Walks the declarative `cmd.Root` tree and emits a versioned JSON description of
every command, flag, example, output shape, and typed error. Consumed by
`docs.baseten.co` to generate the published CLI reference.

This is a developer tool, not a user-facing `baseten` subcommand. Its only
caller is the `docs.baseten.co` pipeline.

## Output contract

The JSON shape is defined by the Go types in `schema.go`. The top-level
`schema_version` field is "1"; bump `SchemaVersion` in `schema.go` whenever an
existing field is removed or its meaning changes (adding a new optional field
does **not** require a bump).

`group_pri` is emitted verbatim: `0` means the flag set no `group-pri`, and the
consumer applies the framework default (`DefaultFlagGroupPri = 100`). The walker
does not resolve it.

## Running locally

```sh
# Write to stdout (default).
go run ./internal/cmddocs --cli-version=dev

# Write to a file.
go run ./internal/cmddocs --cli-version=v0.1.0 --out=docs.json

# Reproducible timestamp.
SOURCE_DATE_EPOCH=1700000000 go run ./internal/cmddocs --cli-version=v0.1.0 --out=docs.json
```

## Tests

```sh
go test ./internal/cmddocs/...
```

Unit tests exercise the walker over synthetic command trees and assert the
emitted JSON is valid and stable. There is no committed snapshot to maintain:
schema drift surfaces in the `docs.baseten.co` pipeline, which regenerates the
reference against a pinned baseten-cli ref and opens a PR on any change.

## How `docs.baseten.co` consumes this

The docs pipeline pins a baseten-cli ref, checks it out, and runs
`go run ./internal/cmddocs --cli-version=<tag> --out=docs.json`, then feeds the
result to its MDX generator. Nothing in this repo's release flow runs the
emitter or publishes `docs.json`.
