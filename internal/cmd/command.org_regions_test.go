package cmd_test

import (
	"net/http"
	"strings"
	"testing"
)

func Test_Org_Regions_Empty(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/regions", 200, map[string]any{"regions": []any{}})

	h.Require.NoError(h.Execute("org", "regions"))
	h.Require.Equal("", h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "No regions found.")
}

func Test_Org_Regions_SortedBySlug(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/regions", 200, map[string]any{
		"regions": []any{
			map[string]any{"slug": "us-west-2", "display_name": "US West (Oregon)"},
			map[string]any{"slug": "eu-central-1", "display_name": "Europe (Frankfurt)"},
		},
	})

	h.Require.NoError(h.Execute("org", "regions"))
	out := h.Stdout.String()
	h.Require.Contains(out, "SLUG")
	h.Require.Contains(out, "NAME")
	h.Require.Contains(out, "US West (Oregon)")
	h.Require.Contains(out, "eu-central-1")
	h.Require.Contains(out, "us-west-2")
	h.Require.Less(strings.Index(out, "eu-central-1"), strings.Index(out, "us-west-2"))
}

func Test_Org_Regions_JSON(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/regions", 200, map[string]any{
		"regions": []any{map[string]any{"slug": "us-west-2", "display_name": "US West (Oregon)"}},
	})

	h.Require.NoError(h.Execute("org", "regions", "--output", "json"))
	h.Require.Contains(h.Stdout.String(), `"slug": "us-west-2"`)
}

func Test_Org_Regions_Team(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/teams", 200, map[string]any{
		"teams": []any{teamFixture("t1", "Engineering", true)},
	})
	m.SetRoute("GET", "/v1/teams/t1/regions", 200, map[string]any{
		"regions": []any{map[string]any{"slug": "eu-central-1", "display_name": "Europe (Frankfurt)"}},
	})

	// The team name is resolved to its ID before the regions call.
	h.Require.NoError(h.Execute("org", "regions", "--team", "Engineering"))
	h.Require.Contains(h.Stdout.String(), "eu-central-1")
	h.Require.NotNil(m.FindCall("GET", "/v1/teams/t1/regions"))
}

func Test_Org_Regions_Team_NotFound(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/teams", 200, map[string]any{"teams": []any{}})
	m.SetHandlerFallback(func(_ http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected call to %s", r.URL.Path)
	})

	err := h.Execute("org", "regions", "--team", "ghost")
	h.Require.ErrorContains(err, `no team matched "ghost"`)
}
