package cmd_test

import (
	"encoding/json"
	"testing"
)

// sandboxTestVolume is a control plane sandbox volume record.
func sandboxTestVolume(status string) map[string]any {
	return map[string]any{
		"name": "vol", "size": "10Gi", "region": "us-was-1", "status": status, "attached_to": "sbx-1",
		"labels": map[string]string{"b": "2", "a": "1"}, "created_at": sandboxTestTime, "created_by": "me",
	}
}

func Test_Sandbox_Volume_List_FiltersAndTable(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	unattached := sandboxTestVolume("DEPLOYED")
	unattached["name"], unattached["size"] = "other", 512
	delete(unattached, "attached_to")
	m.SetRoute("GET", "/v1/sandboxes/volumes", 200, sandboxTestPage("", sandboxTestVolume("DEPLOYING"), unattached))

	h.Require.NoError(h.Execute("sandbox", "volume", "list", "--query", "vo", "--status", "deployed", "--status", "failed"))
	query := sandboxTestCalls(m, "GET", "/v1/sandboxes/volumes")[0].Query()
	h.Require.Equal("vo", query.Get("q"))
	h.Require.Equal([]string{"DEPLOYED", "FAILED"}, query["status"])
	out := h.Stdout.String()
	h.Require.Contains(out, "ATTACHED TO")
	h.Require.Regexp(`vol\s+DEPLOYING\s+10Gi\s+us-was-1\s+sbx-1`, out)
	// A size in Mi is shown with its unit, and an unattached volume with a dash.
	h.Require.Regexp(`other\s+DEPLOYED\s+512Mi\s+us-was-1\s+-`, out)
}

func Test_Sandbox_Volume_List_Empty(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRoute("GET", "/v1/sandboxes/volumes", 200, sandboxTestPage(""))

	h.Require.NoError(h.Execute("sandbox", "volume", "list"))
	h.Require.Contains(h.Stderr.String(), "No sandbox volumes found.")

	h.Require.NoError(h.Execute("sandbox", "volume", "list", "--output", "json"))
	var listed map[string]any
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &listed))
	h.Require.Equal(map[string]any{"items": []any{}}, listed)
}

func Test_Sandbox_Volume_Describe_Text(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRoute("GET", "/v1/sandboxes/volumes/vol", 200, sandboxTestVolume("DEPLOYED"))

	h.Require.NoError(h.Execute("sandbox", "volume", "describe", "--name", "vol"))
	out := h.Stdout.String()
	h.Require.Contains(out, "Name:         vol\nStatus:       DEPLOYED\nSize:         10Gi\n")
	h.Require.Contains(out, "Attached to:  sbx-1\n")
	h.Require.Contains(out, "Labels:       a=1, b=2\n")
	h.Require.Contains(out, "Created by:   me\n")
	// Empty fields are left out.
	h.Require.NotContains(out, "Updated")
}

func Test_Sandbox_Volume_Create_SendsOnlySetFields(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRoute("POST", "/v1/sandboxes/volumes", 201, sandboxTestVolume("DEPLOYING"))

	h.Require.NoError(h.Execute("sandbox", "volume", "create", "--size", "10Gi"))
	h.Require.Equal(map[string]any{"size": "10Gi"}, sandboxTestCalls(m, "POST", "/v1/sandboxes/volumes")[0].BodyJSON(t))

	// A bare integer is sent as a number of Mi.
	h.Require.NoError(h.Execute("sandbox", "volume", "create", "--name", "vol", "--size", "512",
		"--region", "us-was-1", "--label", "team=cli"))
	h.Require.Equal(map[string]any{
		"name": "vol", "size": float64(512), "region": "us-was-1", "labels": map[string]any{"team": "cli"},
	}, sandboxTestCalls(m, "POST", "/v1/sandboxes/volumes")[1].BodyJSON(t))

	h.Require.ErrorContains(h.Execute("sandbox", "volume", "create"), "size")
}

func Test_Sandbox_Volume_Update_SendsOnlySetFields(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRoute("PATCH", "/v1/sandboxes/volumes/vol", 200, sandboxTestVolume("DEPLOYED"))

	h.Require.NoError(h.Execute("sandbox", "volume", "update", "--name", "vol", "--size", "20Gi"))
	h.Require.NoError(h.Execute("sandbox", "volume", "update", "--name", "vol", "--size", "20480", "--label", "env=dev"))
	// Sent empty, not left out, so the server removes them all.
	h.Require.NoError(h.Execute("sandbox", "volume", "update", "--name", "vol", "--clear-labels"))
	calls := sandboxTestCalls(m, "PATCH", "/v1/sandboxes/volumes/vol")
	h.Require.Equal(map[string]any{"size": "20Gi"}, calls[0].BodyJSON(t))
	h.Require.Equal(map[string]any{"size": float64(20480), "labels": map[string]any{"env": "dev"}}, calls[1].BodyJSON(t))
	h.Require.Equal(map[string]any{"labels": map[string]any{}}, calls[2].BodyJSON(t))

	h.Require.ErrorContains(h.Execute("sandbox", "volume", "update", "--name", "vol", "--clear-labels", "--label", "a=1"), "cannot be combined")
	h.Require.Len(sandboxTestCalls(m, "PATCH", "/v1/sandboxes/volumes/vol"), 3)
}

func Test_Sandbox_Volume_Delete_ConfirmsAndLogsToStderr(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRoute("DELETE", "/v1/sandboxes/volumes/vol", 202, sandboxTestVolume("DELETING"))

	// The harness stdin is not a terminal, so the prompt refuses.
	h.Require.ErrorContains(h.Execute("sandbox", "volume", "delete", "--name", "vol"), "--yes")
	h.Require.Empty(sandboxTestCalls(m, "DELETE", "/v1/sandboxes/volumes/vol"))

	h.Require.NoError(h.Execute("sandbox", "volume", "delete", "--name", "vol", "--yes"))
	h.Require.Empty(h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "Deleting sandbox volume vol.")
}
