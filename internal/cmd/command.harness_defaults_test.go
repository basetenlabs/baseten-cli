package cmd_test

import (
	"encoding/json"
	"testing"

	public "github.com/basetenlabs/baseten-cli/cmd"
	"github.com/stretchr/testify/require"
)

func Test_Harness_HarnessDefaults_All(t *testing.T) {
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

	require.NoError(t, h.Execute("harness", "harness-defaults", "--output", "json"))

	var out public.HarnessDefaultsList
	require.NoError(t, json.Unmarshal([]byte(h.Stdout.String()), &out))
	require.Equal(t, "team-a", out.TeamId)
	require.Equal(t, "Engineering", out.TeamName)
	require.Len(t, out.Items, 4)

	byHarness := map[string]public.HarnessDefault{}
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

func Test_Harness_HarnessDefaults_Filter(t *testing.T) {
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

	require.NoError(t, h.Execute("harness", "harness-defaults", "--harness", "codex", "--output", "json"))

	var out public.HarnessDefaultsList
	require.NoError(t, json.Unmarshal([]byte(h.Stdout.String()), &out))
	require.Len(t, out.Items, 1)
	require.Equal(t, "codex", out.Items[0].Harness)
	require.Equal(t, "acme/gpt", out.Items[0].Primary.Route.Name)
	require.Nil(t, out.Items[0].Background)
}

func Test_Harness_HarnessDefaults_Text(t *testing.T) {
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

	require.NoError(t, h.Execute("harness", "harness-defaults"))
	out := h.Stdout.String()

	require.Contains(t, out, "claude-code")
	require.Contains(t, out, "acme/claude (team)")
	require.Contains(t, out, "acme/fast (team)")
	require.Contains(t, out, "codex")
	require.Contains(t, out, "acme/gpt (team)")
	require.Contains(t, out, "n/a")
	require.Contains(t, out, "-")
}
