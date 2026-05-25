package docs_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	cmdpkg "github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/docs"
)

var updateGolden = flag.Bool("update", false, "Rewrite internal/docs/testdata/docs.golden.json from the current cmd.Root.")

func Test_Golden_RootMatches(t *testing.T) {
	// Use fixed inputs so the golden file is reproducible.
	schema := docs.Walk("v0.0.0-golden", "2026-01-01T00:00:00Z", cmdpkg.Root)
	got, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got = append(got, '\n')

	path := filepath.Join("testdata", "docs.golden.json")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("wrote %s", path)
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run `go test ./internal/docs -run Golden -update` to create): %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("docs.golden.json drift. If intentional, run:\n  go test ./internal/docs -run Golden -update\n\nFirst 400 chars of got:\n%s", truncate(string(got), 400))
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
