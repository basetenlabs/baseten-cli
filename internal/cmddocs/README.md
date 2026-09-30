# internal/cmddocs

Walks the declarative `cmd.Root` tree and emits command descriptions, flags,
examples, output metadata, and typed errors as JSON. The generator in
`docs.baseten.co` uses this metadata to build CLI reference snippets and page
drafts.

Run this developer tool from a checkout of the release being documented. It
does not load credentials or execute CLI commands.

## Output contract

The JSON shape is defined by the Go types in `schema.go`. The top-level
`schema_version` field is "1"; bump `SchemaVersion` in `schema.go` whenever an
existing field is removed or its meaning changes (adding a new optional field
does **not** require a bump).

`group_pri` is emitted verbatim: `0` means the flag set no `group-pri`, and the
consumer applies the framework default (`DefaultFlagGroupPri = 100`). The walker
does not resolve it.

The emitter preserves the complete declared command tree, including hidden
commands and flags. Public reference generators must exclude commands marked
`hidden`, their descendants, and flags marked `hidden`. Keeping this metadata
lets consumers follow the visibility declared by the CLI's authors.

Optional metadata fields are omitted when unset:

- `hidden` marks commands and flags excluded from public help.
- `nullable` means a flag accepts the literal `null`, including when its enum
  lists only non-null values.
- `json_output_unimportant` marks commands without meaningful JSON output.
- `json_alternatives` lists additional output types and the input selecting
  each one. Each entry contains `when` and `json_output_type`.

Output types are Go type names, not JSON field schemas. For streamed commands,
`json_array_streamed` means the type describes one record. Examples use the
same command text as the CLI, joining multiline declarations into one line.

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

Tests cover metadata extraction, the real command tree, file and stdout output,
reproducible timestamps, and error handling. They run with the repository's
normal Go tests. There is no generated snapshot to keep in sync.

## How `docs.baseten.co` consumes this

Docs maintainers check out a released CLI tag and run
`go run ./internal/cmddocs --cli-version=<tag> --out=docs.json`. In the docs
checkout, they pass the file to `bin/generate_baseten_cli_docs.py` and review
the generated changes before publishing. Generation is a manual maintenance
step. Nothing in this repo's release flow runs the emitter or publishes
`docs.json`.
