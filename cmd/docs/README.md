# cmd/docs

Walks the declarative `cmd.Root` tree and emits a versioned JSON description of
every command, flag, example, output shape, and typed error. Consumed by
`docs.baseten.co` to generate the published CLI reference.

## Output contract

The JSON shape is defined by the Go types in `internal/docs/schema.go`. The
top-level `schema_version` field is "1"; bump `SchemaVersion` in `schema.go`
whenever an existing field is removed or its meaning changes (adding a new
optional field does **not** require a bump).

Consumers pin a specific baseten-cli release and download `docs.json` from
that release's assets.

## Running locally

```sh
# Write to stdout (default).
go run ./cmd/docs --cli-version=dev

# Write to a file.
go run ./cmd/docs --cli-version=v0.1.0 --out=docs.json

# Reproducible timestamp (goreleaser sets SOURCE_DATE_EPOCH automatically).
SOURCE_DATE_EPOCH=1700000000 go run ./cmd/docs --cli-version=v0.1.0 --out=docs.json
```

## Tests

`go test ./internal/docs/...` runs unit tests plus a golden snapshot of the
real `cmd.Root` output. If a schema or command tree change is intentional,
regenerate the golden file:

```sh
go test ./internal/docs -run Golden -update
```

Then review the diff in `internal/docs/testdata/docs.golden.json` before
committing.

## How releases publish `docs.json`

`.goreleaser.yaml` runs `go run ./cmd/docs --cli-version={{.Version}} --out=docs.json`
in its `before.hooks` and attaches the result to each GitHub release via
`release.extra_files`. CI also re-runs the golden test on every PR to catch
unintended drift before tagging.
