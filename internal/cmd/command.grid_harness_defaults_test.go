package cmd_test

import (
	"encoding/json"
	"fmt"
	"testing"

	public "github.com/basetenlabs/baseten-cli/cmd"
	"github.com/stretchr/testify/require"
)

func Test_Grid_HarnessDefaults_All(t *testing.T) {
	skipUnlessSupported(t)
	h, api, _ := harnessDefaultsAPI(t, nil)
	api.SetRoute("GET", harnessDefaultsPath, 200, harnessDefaultsBody(map[string]any{
		"claude_code": map[string]any{
			"primary":    harnessDefaultModel("team", harnessRoute(api, "claude")),
			"background": harnessDefaultModel("team", harnessRoute(api, "fast")),
		},
		"codex": map[string]any{
			"primary": harnessDefaultModel("team", harnessRoute(api, "gpt")),
		},
		"opencode": map[string]any{
			"primary":    harnessDefaultModel("team", harnessRoute(api, "gpt")),
			"background": harnessDefaultModel("team", harnessRoute(api, "claude")),
		},
		"pi": map[string]any{
			"primary": harnessDefaultModel("team", harnessRoute(api, "claude")),
		},
	}))

	require.NoError(t, h.Execute("grid", "harness-defaults", "--output", "json"))

	var out public.GridHarnessDefaultsList
	require.NoError(t, json.Unmarshal([]byte(h.Stdout.String()), &out))
	require.Equal(t, "team-a", out.TeamId)
	require.Equal(t, "Engineering", out.TeamName)
	require.Len(t, out.Items, 4)

	byHarness := map[string]public.GridHarnessDefault{}
	for _, item := range out.Items {
		byHarness[item.Harness] = item
	}
	require.Equal(t, "acme/claude", byHarness["claude-code"].Primary.Route.Name)
	require.Equal(t, "team", string(byHarness["claude-code"].Primary.Source))
	require.Equal(t, "acme/fast", byHarness["claude-code"].Background.Route.Name)
	require.Equal(t, "acme/gpt", byHarness["codex"].Primary.Route.Name)
	require.Nil(t, byHarness["codex"].Background)
	require.Equal(t, "acme/gpt", byHarness["opencode"].Primary.Route.Name)
	require.Equal(t, "acme/claude", byHarness["opencode"].Background.Route.Name)
	require.Equal(t, "acme/claude", byHarness["pi"].Primary.Route.Name)
	require.Nil(t, byHarness["pi"].Background)
}

func Test_Grid_HarnessDefaults_Filter(t *testing.T) {
	skipUnlessSupported(t)
	h, api, _ := harnessDefaultsAPI(t, nil)
	api.SetRoute("GET", harnessDefaultsPath, 200, harnessDefaultsBody(map[string]any{
		"claude_code": map[string]any{
			"primary": harnessDefaultModel("team", harnessRoute(api, "claude")),
		},
		"codex": map[string]any{
			"primary": harnessDefaultModel("team", harnessRoute(api, "gpt")),
		},
	}))

	require.NoError(t, h.Execute("grid", "harness-defaults", "--harness", "codex", "--output", "json"))

	var out public.GridHarnessDefaultsList
	require.NoError(t, json.Unmarshal([]byte(h.Stdout.String()), &out))
	require.Len(t, out.Items, 1)
	require.Equal(t, "codex", out.Items[0].Harness)
	require.Equal(t, "acme/gpt", out.Items[0].Primary.Route.Name)
	require.Nil(t, out.Items[0].Background)
}

func Test_Grid_HarnessDefaults_Text(t *testing.T) {
	skipUnlessSupported(t)
	h, api, _ := harnessDefaultsAPI(t, nil)
	api.SetRoute("GET", harnessDefaultsPath, 200, harnessDefaultsBody(map[string]any{
		"claude_code": map[string]any{
			"primary":    harnessDefaultModel("team", harnessRoute(api, "claude")),
			"background": harnessDefaultModel("team", harnessRoute(api, "fast")),
		},
		"codex": map[string]any{
			"primary": harnessDefaultModel("team", harnessRoute(api, "gpt")),
		},
		"opencode": map[string]any{
			"primary": harnessDefaultModel("team", harnessRoute(api, "gpt")),
		},
		"pi": map[string]any{},
	}))

	require.NoError(t, h.Execute("grid", "harness-defaults"))
	out := h.Stdout.String()

	require.Contains(t, out, "claude-code")
	require.Contains(t, out, "acme/claude (team)")
	require.Contains(t, out, "acme/fast (team)")
	require.Contains(t, out, "codex")
	require.Contains(t, out, "acme/gpt (team)")
	require.Contains(t, out, "n/a")
	require.Contains(t, out, "-")
}

func Test_Grid_HarnessDefaults_UsesResolvedTeamSettingsEndpoint(t *testing.T) {
	h, api, _ := harnessDefaultsAPI(t, nil)
	h.Require.NoError(h.Execute("grid", "harness-defaults", "--output", "json"))
	call := api.FindCall("GET", "/v1/grid/team_settings")
	h.Require.NotNil(call)
	h.Require.NotContains(call.Query(), "team_id")
	h.Require.Nil(api.FindCall("GET", "/v1/teams"))
	h.Require.Nil(api.FindCall("GET", "/v1/grid/settings/teams/team-a"))
}

func Test_Grid_HarnessDefaults_ExplicitTeam(t *testing.T) {
	for _, team := range []string{"team-a", "Engineering"} {
		t.Run(team, func(t *testing.T) {
			h, api, _ := harnessDefaultsAPI(t, nil)
			h.Require.NoError(h.Execute("grid", "harness-defaults", "--team", team, "--output", "json"))
			h.Require.Equal("team-a", api.FindCall("GET", harnessDefaultsPath).Query().Get("team_id"))
			var got public.GridHarnessDefaultsList
			h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &got))
			h.Require.Equal("team-a", got.TeamId)
			h.Require.Equal("Engineering", got.TeamName)
		})
	}
}

func Test_Grid_HarnessDefaults_ServerResolvesTeam(t *testing.T) {
	h, api, _ := harnessDefaultsAPI(t, nil)
	api.SetRoute("GET", "/v1/teams", 500, map[string]string{"message": "unavailable"})
	body := harnessDefaultsBody(nil)
	body["team_id"], body["team_name"] = "team-b", "Research"
	api.SetRoute("GET", harnessDefaultsPath, 200, body)
	h.Require.NoError(h.Execute("grid", "harness-defaults", "--output", "json"))
	var got public.GridHarnessDefaultsList
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &got))
	h.Require.Equal("team-b", got.TeamId)
	h.Require.Equal("Research", got.TeamName)
	h.Require.Len(api.Calls(), 1)
}

func Test_Grid_HarnessDefaults_ServerErrors(t *testing.T) {
	for _, status := range []int{400, 403, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			h, api, _ := harnessDefaultsAPI(t, nil)
			api.SetRoute("GET", harnessDefaultsPath, status, map[string]string{"message": "server rejected team resolution"})
			h.Require.Error(h.Execute("grid", "harness-defaults", "--output", "json"))
			h.Require.Equal(status, decodeJSONErrorEnvelope(h).APIStatusCode)
			h.Require.Len(api.Calls(), 1)
		})
	}
}
