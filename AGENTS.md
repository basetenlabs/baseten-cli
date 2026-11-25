# Agent guidance

Before adding or changing a command, read [CONTRIBUTING.md](CONTRIBUTING.md): file and test naming, command design, output, and how commands call the API. Before cutting a release, read its "Releasing" section.

Before opening a pull request, run CI's checks from the repository root: `go mod tidy && go fmt ./... && git diff --exit-code` (no output means clean), then `go test ./...`.
