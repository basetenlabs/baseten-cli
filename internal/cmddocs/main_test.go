package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func Test_Run_Stdout(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "1700000000")
	for _, args := range [][]string{nil, {"--out=-"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 0 {
			t.Fatalf("run(%v) = %d, stderr: %s", args, code, &stderr)
		}
		if stderr.Len() != 0 {
			t.Fatalf("stderr = %q, want empty", &stderr)
		}
		schema := decodeEmittedSchema(t, stdout.Bytes())
		if schema.CLIVersion != "dev" || schema.GeneratedAt != "2023-11-14T22:13:20Z" {
			t.Fatalf("metadata = version %q, timestamp %q", schema.CLIVersion, schema.GeneratedAt)
		}
	}
}

func Test_Run_FileOutputIsReproducible(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "1700000000")
	path := filepath.Join(t.TempDir(), "docs.json")
	var previous []byte
	for i := 0; i < 2; i++ {
		if i > 0 {
			// A rerun must truncate any bytes beyond the new payload.
			if err := os.WriteFile(path, append(previous, []byte("stale bytes\n")...), 0600); err != nil {
				t.Fatal(err)
			}
		}
		var stdout, stderr bytes.Buffer
		if code := run([]string{"--cli-version=v0.1.0-test", "--out=" + path}, &stdout, &stderr); code != 0 {
			t.Fatalf("run = %d, stderr: %s", code, &stderr)
		}
		if stdout.Len() != 0 || stderr.Len() != 0 {
			t.Fatalf("file output wrote stdout %q or stderr %q", &stdout, &stderr)
		}
		payload, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		schema := decodeEmittedSchema(t, payload)
		if schema.CLIVersion != "v0.1.0-test" || schema.GeneratedAt != "2023-11-14T22:13:20Z" {
			t.Fatalf("metadata = version %q, timestamp %q", schema.CLIVersion, schema.GeneratedAt)
		}
		if i > 0 && !bytes.Equal(payload, previous) {
			t.Fatal("fixed SOURCE_DATE_EPOCH produced different output on a second run")
		}
		previous = payload
	}
}

func Test_Run_CurrentTimestamp(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "")
	before := time.Now().UTC().Truncate(time.Second)
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("run = %d, stderr: %s", code, &stderr)
	}
	after := time.Now().UTC()
	schema := decodeEmittedSchema(t, stdout.Bytes())
	generatedAt, err := time.Parse(time.RFC3339, schema.GeneratedAt)
	if err != nil {
		t.Fatalf("generated_at = %q: %v", schema.GeneratedAt, err)
	}
	if !strings.HasSuffix(schema.GeneratedAt, "Z") || generatedAt.Before(before) || generatedAt.After(after) {
		t.Fatalf("generated_at = %q, want UTC time between %s and %s", schema.GeneratedAt, before, after)
	}
}

func Test_Run_InvalidSourceDateEpoch(t *testing.T) {
	for _, epoch := range []string{"not-an-integer", "1.5", "9223372036854775808"} {
		t.Run(epoch, func(t *testing.T) {
			t.Setenv("SOURCE_DATE_EPOCH", epoch)
			path := filepath.Join(t.TempDir(), "docs.json")
			original := []byte("keep existing output\n")
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			if code := run([]string{"--out=" + path}, &stdout, &stderr); code != 2 {
				t.Fatalf("run = %d, want 2", code)
			}
			if stdout.Len() != 0 || !strings.Contains(stderr.String(), "invalid SOURCE_DATE_EPOCH") || !strings.Contains(stderr.String(), epoch) {
				t.Fatalf("stdout = %q, stderr = %q", &stdout, &stderr)
			}
			payload, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(payload, original) {
				t.Fatal("invalid SOURCE_DATE_EPOCH changed the output file")
			}
		})
	}
}

func Test_Run_OutputErrors(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "1700000000")
	t.Run("stdout", func(t *testing.T) {
		var stderr bytes.Buffer
		if code := run(nil, failingWriter{}, &stderr); code != 1 {
			t.Fatalf("run = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "write: output unavailable") {
			t.Fatalf("stderr = %q", &stderr)
		}
	})
	for name, path := range map[string]string{
		"directory":      t.TempDir(),
		"missing parent": filepath.Join(t.TempDir(), "missing", "docs.json"),
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run([]string{"--out=" + path}, &stdout, &stderr); code != 1 {
				t.Fatalf("run = %d, want 1", code)
			}
			if stdout.Len() != 0 || !strings.Contains(stderr.String(), "write "+path+":") {
				t.Fatalf("stdout = %q, stderr = %q", &stdout, &stderr)
			}
		})
	}
}

func Test_Run_Flags(t *testing.T) {
	// Help and flag errors must return before trying to emit a document.
	t.Setenv("SOURCE_DATE_EPOCH", "invalid")
	for _, test := range []struct {
		name string
		args []string
		code int
		want string
	}{
		{"help", []string{"--help"}, 0, "Usage of cmddocs:"},
		{"unknown", []string{"--unknown"}, 2, "flag provided but not defined"},
		{"missing value", []string{"--out"}, 2, "flag needs an argument"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(test.args, &stdout, &stderr); code != test.code {
				t.Fatalf("run = %d, want %d", code, test.code)
			}
			if stdout.Len() != 0 || !strings.Contains(stderr.String(), test.want) || strings.Contains(stderr.String(), "SOURCE_DATE_EPOCH") {
				t.Fatalf("stdout = %q, stderr = %q", &stdout, &stderr)
			}
		})
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("output unavailable")
}

func decodeEmittedSchema(t *testing.T, payload []byte) Schema {
	t.Helper()
	if !bytes.HasSuffix(payload, []byte("\n")) {
		t.Fatal("output has no trailing newline")
	}
	var schema Schema
	if err := json.Unmarshal(payload, &schema); err != nil {
		t.Fatalf("invalid output JSON: %v", err)
	}
	if schema.SchemaVersion != "1" || schema.Root.Name != "baseten" || schema.Root.IsLeaf || len(schema.StandardErrors) == 0 {
		t.Fatalf("missing schema metadata or real command tree: version %q, root %q, leaf %v, standard errors %d",
			schema.SchemaVersion, schema.Root.Name, schema.Root.IsLeaf, len(schema.StandardErrors))
	}
	leaf := schema.Root
	for _, name := range []string{"train", "job", "list"} {
		index := slices.IndexFunc(leaf.Children, func(child Command) bool { return child.Name == name })
		if index < 0 {
			t.Fatalf("missing command %q under %v", name, leaf.Path)
		}
		leaf = leaf.Children[index]
	}
	if !leaf.IsLeaf || !slices.Equal(leaf.Path, []string{"baseten", "train", "job", "list"}) {
		t.Fatalf("training job list has leaf %v and path %v", leaf.IsLeaf, leaf.Path)
	}
	if len(leaf.Flags) == 0 || len(leaf.Examples) == 0 || leaf.JSONOutputType == "" {
		t.Fatal("training job list is missing flags, examples, or its JSON output type")
	}
	return schema
}
