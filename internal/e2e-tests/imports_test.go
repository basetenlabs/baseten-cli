package e2etests

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestE2EUsesCLIOnlyThroughHelpers keeps the e2e tests using the CLI the way a
// user would: only helpers_test.go may import this repository's packages.
// Untagged, so it runs with the unit tests.
func TestE2EUsesCLIOnlyThroughHelpers(t *testing.T) {
	// This file's own directory, rather than the working directory.
	_, self, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	files, err := filepath.Glob(filepath.Join(filepath.Dir(self), "*.go"))
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, file := range files {
		name := filepath.Base(file)
		if name == "helpers_test.go" {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		require.NoError(t, err)
		for _, spec := range parsed.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			require.NoError(t, err)
			require.False(t, strings.HasPrefix(path, "github.com/basetenlabs/baseten-cli/"),
				"%s imports %s; only helpers_test.go may import this repository's packages", name, path)
		}
	}
}
