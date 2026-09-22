package cmd_test

import (
	"encoding/json"
	"testing"

	internalcmd "github.com/basetenlabs/baseten-cli/internal/cmd"
	"github.com/stretchr/testify/require"
)

func harnessRouteFixture(t *testing.T, name string, metadata any) internalcmd.HarnessRouteRecordForTest {
	t.Helper()
	route := map[string]any{"id": "route-" + name, "name": name, "team_id": "team-a", "display_name": "Display " + name, "invoke_url": "http://127.0.0.1:1234", "metadata": metadata}
	encoded, err := json.Marshal([]map[string]any{route})
	require.NoError(t, err)
	var records []internalcmd.HarnessRouteRecordForTest
	require.NoError(t, json.Unmarshal(encoded, &records))
	return records[0]
}

func Test_Harness_Setup_HarnessCatalogReadsRouteMetadata(t *testing.T) {
	metadata := map[string]any{
		"slug": "openai/gpt-5.2", "context_window": 400000, "max_output_tokens": 128000,
		"input_modalities": []string{"text", "image"}, "tools": true,
		"reasoning_effort_levels": []string{"minimal", "low", "medium", "high", "xhigh"},
		"parallel_tool_calls":     true,
		"supported_api_formats":   map[string]any{"messages": false, "responses": true, "chat_completions": false},
	}
	listed := []internalcmd.HarnessRouteRecordForTest{
		harnessRouteFixture(t, "acme/primary", metadata),
		harnessRouteFixture(t, "acme/unresolved", nil),
	}
	routes, skipped, err := internalcmd.HarnessCatalogForTest(listed, "acme/primary")
	require.NoError(t, err)
	require.Equal(t, []string{"acme/unresolved"}, skipped)
	require.Len(t, routes, 1)
	require.Equal(t, "acme/primary", routes[0].Name)
	require.Equal(t, "Display acme/primary", routes[0].DisplayName)
	require.Equal(t, 400000, routes[0].ContextWindow)
	require.Equal(t, 128000, routes[0].OutputLimit)
	require.Equal(t, []string{"text", "image"}, routes[0].InputModalities)
	require.True(t, routes[0].Tools)
	require.Equal(t, []string{"minimal", "low", "medium", "high", "xhigh"}, routes[0].ReasoningLevels)
	require.True(t, routes[0].ParallelTools)
	require.False(t, routes[0].Messages)
	require.True(t, routes[0].Responses)
	require.False(t, routes[0].ChatCompletions)
}

func Test_Harness_Setup_HarnessCatalogSelectedPrimaryWithoutMetadataFails(t *testing.T) {
	listed := []internalcmd.HarnessRouteRecordForTest{harnessRouteFixture(t, "acme/primary", nil)}
	_, _, err := internalcmd.HarnessCatalogForTest(listed, "acme/primary")
	require.ErrorContains(t, err, `baseten route update --name acme/primary --metadata`)
}

func Test_Harness_Setup_HarnessCatalogRejectsUnusableMetadata(t *testing.T) {
	withoutTools := map[string]any{"context_window": 128000, "max_output_tokens": 4096, "input_modalities": []string{"text"}, "tools": false, "supported_api_formats": map[string]any{"messages": true, "responses": true, "chat_completions": true}}
	listed := []internalcmd.HarnessRouteRecordForTest{
		harnessRouteFixture(t, "acme/no-tools", withoutTools),
		harnessRouteFixture(t, "acme/unresolved", nil),
	}
	_, skipped, err := internalcmd.HarnessCatalogForTest(listed, "")
	require.ErrorContains(t, err, "baseten route update --name <route> --metadata")
	require.ElementsMatch(t, []string{"acme/no-tools", "acme/unresolved"}, skipped)
}
