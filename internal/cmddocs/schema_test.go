package main

import (
	"encoding/json"
	"testing"
)

func Test_Schema_VersionIsStable(t *testing.T) {
	if SchemaVersion != "1" {
		t.Fatalf("SchemaVersion = %q, want %q (bump deliberately and update docs.baseten.co consumer)", SchemaVersion, "1")
	}
}

func Test_Schema_MarshalsEmpty(t *testing.T) {
	s := Schema{
		SchemaVersion: SchemaVersion,
		CLIVersion:    "v0.0.0-test",
		GeneratedAt:   "2026-01-01T00:00:00Z",
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	want := `{
  "schema_version": "1",
  "cli_version": "v0.0.0-test",
  "generated_at": "2026-01-01T00:00:00Z",
  "standard_errors": null,
  "root": {
    "name": "",
    "path": null,
    "summary": "",
    "description": "",
    "is_leaf": false,
    "args_usage": "",
    "exact_args": 0,
    "max_args": 0,
    "disable_flag_parsing": false,
    "flags": null,
    "examples": null,
    "jq_example": null,
    "text_description": "",
    "json_description": "",
    "json_output_type": "",
    "json_array_streamed": false,
    "errors": null,
    "children": null
  }
}`
	if got != want {
		t.Fatalf("JSON mismatch.\nGot:\n%s\nWant:\n%s", got, want)
	}
}
