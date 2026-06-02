package docs_test

import (
	"testing"

	cmdpkg "github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/docs"
)

func Test_Walk_LeafBasicFields(t *testing.T) {
	leaf := cmdpkg.Command{
		Name:        "ping",
		Summary:     "Ping the server",
		Description: "Sends a ping.",
		Output:      &cmdpkg.CommandOutput[cmdpkg.JSONUndefined]{},
	}
	got := docs.WalkCommand([]string{"baseten"}, leaf)
	if got.Name != "ping" || got.Summary != "Ping the server" || got.Description != "Sends a ping." {
		t.Fatalf("basic fields wrong: %+v", got)
	}
	wantPath := []string{"baseten", "ping"}
	if len(got.Path) != len(wantPath) || got.Path[0] != "baseten" || got.Path[1] != "ping" {
		t.Fatalf("path = %v, want %v", got.Path, wantPath)
	}
	if !got.IsLeaf {
		t.Fatalf("IsLeaf = false, want true (no Children)")
	}
}

func Test_Walk_RecursesIntoChildren(t *testing.T) {
	tree := cmdpkg.Command{
		Name:    "auth",
		Summary: "Authentication",
		Children: []cmdpkg.Command{
			{Name: "login", Summary: "Log in", Output: &cmdpkg.CommandOutput[cmdpkg.JSONUndefined]{}},
			{Name: "logout", Summary: "Log out", Output: &cmdpkg.CommandOutput[cmdpkg.JSONUndefined]{}},
		},
	}
	got := docs.WalkCommand([]string{"baseten"}, tree)
	if got.IsLeaf {
		t.Fatalf("IsLeaf = true for parent, want false")
	}
	if len(got.Children) != 2 {
		t.Fatalf("Children len = %d, want 2", len(got.Children))
	}
	if got.Children[0].Name != "login" || got.Children[1].Name != "logout" {
		t.Fatalf("child names = %q, %q; want login, logout", got.Children[0].Name, got.Children[1].Name)
	}
	wantLoginPath := []string{"baseten", "auth", "login"}
	if !sliceEq(got.Children[0].Path, wantLoginPath) {
		t.Fatalf("login path = %v, want %v", got.Children[0].Path, wantLoginPath)
	}
}

func sliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type pingFlags struct {
	cmdpkg.CommandFlags
	Count int    `flag:"count" short:"n" desc:"Number of pings" default:"3" group:"command"`
	Tag   string `flag:"tag" desc:"Optional tag" enum:"a,b,c"`
	All   bool   `flag:"all" desc:"Ping all hosts" required:"true"`
}

func Test_Walk_FlagsExtracted(t *testing.T) {
	leaf := cmdpkg.Command{
		Name:   "ping",
		Flags:  pingFlags{},
		Output: &cmdpkg.CommandOutput[cmdpkg.JSONUndefined]{},
	}
	got := docs.WalkCommand([]string{"baseten"}, leaf)

	byName := map[string]docs.Flag{}
	for _, f := range got.Flags {
		byName[f.Name] = f
	}

	count, ok := byName["count"]
	if !ok {
		t.Fatalf("missing flag 'count'; got %v", flagNames(got.Flags))
	}
	if count.Short != "n" || count.Description != "Number of pings" || count.Default != "3" || count.Type != "int" {
		t.Fatalf("count flag wrong: %+v", count)
	}

	tag := byName["tag"]
	if len(tag.Enum) != 3 || tag.Enum[0] != "a" || tag.Enum[2] != "c" {
		t.Fatalf("tag enum wrong: %v", tag.Enum)
	}

	all := byName["all"]
	if !all.Required {
		t.Fatalf("all should be required")
	}
	if all.Type != "bool" {
		t.Fatalf("all type = %q, want %q", all.Type, "bool")
	}

	// Common flags from embedded CommandFlags should be present.
	if _, ok := byName["verbose"]; !ok {
		t.Fatalf("missing embedded 'verbose' flag")
	}
}

func flagNames(fs []docs.Flag) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Name
	}
	return out
}

type pingResult struct {
	Host string `json:"host"`
	OK   bool   `json:"ok"`
}

func Test_Walk_OutputAndExamples(t *testing.T) {
	leaf := cmdpkg.Command{
		Name: "ping",
		Output: &cmdpkg.CommandOutput[pingResult]{
			TextDescription: "Prints \"OK\" on success.",
			JSONDescription: "Returns {host, ok}.",
			Examples: []cmdpkg.CommandExample{
				{Description: "Ping example.com.", Command: "baseten ping example.com"},
			},
			JQExample:         cmdpkg.CommandExample{Description: "Just the host.", Command: "baseten ping example.com --jq '.host'"},
			JSONArrayStreamed: false,
		},
	}
	got := docs.WalkCommand([]string{"baseten"}, leaf)
	if got.TextDescription != "Prints \"OK\" on success." {
		t.Fatalf("TextDescription wrong: %q", got.TextDescription)
	}
	if got.JSONDescription != "Returns {host, ok}." {
		t.Fatalf("JSONDescription wrong: %q", got.JSONDescription)
	}
	if got.JSONArrayStreamed {
		t.Fatalf("JSONArrayStreamed = true, want false")
	}
	if len(got.Examples) != 1 || got.Examples[0].Command != "baseten ping example.com" {
		t.Fatalf("Examples wrong: %+v", got.Examples)
	}
	if got.JQExample == nil {
		t.Fatalf("JQExample = nil, want set")
	}
	if got.JQExample.Command != "baseten ping example.com --jq '.host'" {
		t.Fatalf("JQExample.Command = %q", got.JQExample.Command)
	}
	if got.JSONOutputType != "docs_test.pingResult" {
		t.Fatalf("JSONOutputType = %q, want %q", got.JSONOutputType, "docs_test.pingResult")
	}
}

func Test_Walk_OutputNilForParent(t *testing.T) {
	parent := cmdpkg.Command{
		Name:     "auth",
		Children: []cmdpkg.Command{{Name: "x", Output: &cmdpkg.CommandOutput[cmdpkg.JSONUndefined]{}}},
	}
	got := docs.WalkCommand([]string{"baseten"}, parent)
	if got.TextDescription != "" || got.JQExample != nil || got.JSONOutputType != "" {
		t.Fatalf("parent should have empty output fields: %+v", got)
	}
}

func Test_Walk_PerCommandErrors(t *testing.T) {
	leaf := cmdpkg.Command{
		Name:   "fetch",
		Output: &cmdpkg.CommandOutput[cmdpkg.JSONUndefined]{},
		Errors: []cmdpkg.ErrorDesc{
			{Name: "ErrRateLimited", Code: 7, Meaning: "Rate limit exceeded"},
		},
	}
	got := docs.WalkCommand([]string{"baseten"}, leaf)
	if len(got.Errors) != 1 || got.Errors[0].Name != "ErrRateLimited" || got.Errors[0].Code != 7 || got.Errors[0].Meaning != "Rate limit exceeded" {
		t.Fatalf("Errors wrong: %+v", got.Errors)
	}
}

func Test_Walk_TopLevelMetadata(t *testing.T) {
	root := cmdpkg.Command{
		Name:    "baseten",
		Summary: "Baseten CLI",
		Children: []cmdpkg.Command{
			{Name: "version", Summary: "Print version", Output: &cmdpkg.CommandOutput[cmdpkg.JSONUndefined]{}},
		},
	}
	s := docs.Walk("v9.9.9", "2026-01-01T00:00:00Z", root)
	if s.SchemaVersion != docs.SchemaVersion {
		t.Fatalf("SchemaVersion = %q, want %q", s.SchemaVersion, docs.SchemaVersion)
	}
	if s.CLIVersion != "v9.9.9" {
		t.Fatalf("CLIVersion = %q", s.CLIVersion)
	}
	if s.GeneratedAt != "2026-01-01T00:00:00Z" {
		t.Fatalf("GeneratedAt = %q", s.GeneratedAt)
	}
	if s.Root.Name != "baseten" || len(s.Root.Children) != 1 {
		t.Fatalf("Root wrong: %+v", s.Root)
	}
	if len(s.StandardErrors) == 0 {
		t.Fatalf("StandardErrors is empty; want framework's standard set")
	}
	// Spot-check one standard error code.
	foundAuth := false
	for _, e := range s.StandardErrors {
		if e.Name == "ErrAuth" && e.Code == 3 {
			foundAuth = true
		}
	}
	if !foundAuth {
		t.Fatalf("StandardErrors missing ErrAuth (code 3): %+v", s.StandardErrors)
	}
}
