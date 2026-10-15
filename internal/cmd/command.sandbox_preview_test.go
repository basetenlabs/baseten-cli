package cmd_test

import (
	"testing"
)

const sandboxTestPreviewsPath = "/v1/sandboxes/instances/sbx-1/previews"

// sandboxTestPreview is a control plane preview record.
func sandboxTestPreview(public bool) map[string]any {
	return map[string]any{
		"name": "web", "port": 3000, "url": "https://p.example", "status": "DEPLOYED", "public": public,
		"region": "us-was-1", "expires_at": sandboxTestTime,
		"response_headers": map[string]string{"B": "2", "A": "1"},
		"request_headers":  map[string]string{"Authorization": "****"},
		"created_at":       sandboxTestTime,
	}
}

func Test_Sandbox_Preview_List_Table(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRoute("GET", sandboxTestPreviewsPath, 200, sandboxTestPage("", sandboxTestPreview(false)))

	h.Require.NoError(h.Execute("sandbox", "preview", "list", "--name", "sbx-1"))
	out := h.Stdout.String()
	h.Require.Contains(out, "NAME")
	h.Require.Regexp(`web\s+3000\s+false\s+DEPLOYED\s+https://p\.example\s+2026-09-30T22:00:00Z`, out)
}

func Test_Sandbox_Preview_List_Empty(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRoute("GET", sandboxTestPreviewsPath, 200, sandboxTestPage(""))

	h.Require.NoError(h.Execute("sandbox", "preview", "list", "--name", "sbx-1"))
	h.Require.Empty(h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "No previews found.")
}

func Test_Sandbox_Preview_Describe_ShowSecretsAndText(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRoute("GET", sandboxTestPreviewsPath+"/web", 200, sandboxTestPreview(false))

	h.Require.NoError(h.Execute("sandbox", "preview", "describe", "--name", "sbx-1", "--preview-name", "web"))
	h.Require.Empty(sandboxTestCalls(m, "GET", sandboxTestPreviewsPath+"/web")[0].Query().Get("show_secrets"))
	out := h.Stdout.String()
	h.Require.Contains(out, "URL:               https://p.example\n")
	h.Require.Contains(out, "Port:              3000\n")
	h.Require.Contains(out, "Public:            false\n")
	h.Require.Contains(out, "Response headers:  A=1, B=2\n")
	h.Require.Contains(out, "Request headers:   Authorization=****\n")
	h.Require.Contains(out, "Expires at:        2026-09-30T22:00:00Z\n")
	// Empty fields are left out.
	h.Require.NotContains(out, "Updated")

	h.Require.NoError(h.Execute("sandbox", "preview", "describe", "--name", "sbx-1", "--preview-name", "web", "--show-secrets"))
	h.Require.Equal("true", sandboxTestCalls(m, "GET", sandboxTestPreviewsPath+"/web")[1].Query().Get("show_secrets"))
}

func Test_Sandbox_Preview_Create_SendsOnlySetFields(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRoute("POST", sandboxTestPreviewsPath, 201, sandboxTestPreview(true))

	h.Require.NoError(h.Execute("sandbox", "preview", "create", "--name", "sbx-1", "--port", "3000"))
	h.Require.Equal(map[string]any{"port": float64(3000)}, sandboxTestCalls(m, "POST", sandboxTestPreviewsPath)[0].BodyJSON(t))

	h.Require.NoError(h.Execute("sandbox", "preview", "create", "--name", "sbx-1", "--port", "3000",
		"--preview-name", "web", "--public", "--if-not-exists", "--ttl", "7d",
		"--response-header", "Access-Control-Allow-Origin=*", "--request-header", "Authorization=Bearer x"))
	h.Require.Equal(map[string]any{
		"port": float64(3000), "name": "web", "public": true, "create_if_not_exists": true, "ttl": "168h0m0s",
		"response_headers": map[string]any{"Access-Control-Allow-Origin": "*"},
		"request_headers":  map[string]any{"Authorization": "Bearer x"},
	}, sandboxTestCalls(m, "POST", sandboxTestPreviewsPath)[1].BodyJSON(t))
	h.Require.Contains(h.Stdout.String(), "Name:              web\n")

	h.Require.NoError(h.Execute("sandbox", "preview", "create", "--name", "sbx-1", "--port", "3000",
		"--expires-at", "2026-11-01T12:00:00Z"))
	h.Require.Equal(map[string]any{"port": float64(3000), "expires_at": "2026-11-01T12:00:00Z"},
		sandboxTestCalls(m, "POST", sandboxTestPreviewsPath)[2].BodyJSON(t))

	// A passed 0 is sent for the server to judge, not dropped as unset.
	h.Require.NoError(h.Execute("sandbox", "preview", "create", "--name", "sbx-1", "--port", "3000", "--ttl", "0"))
	h.Require.Equal(map[string]any{"port": float64(3000), "ttl": "0s"},
		sandboxTestCalls(m, "POST", sandboxTestPreviewsPath)[3].BodyJSON(t))
}

func Test_Sandbox_Preview_Create_RequiresPortAndValidHeaders(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	h.Require.ErrorContains(h.Execute("sandbox", "preview", "create", "--name", "sbx-1"), "port")
	h.Require.ErrorContains(h.Execute("sandbox", "preview", "create", "--name", "sbx-1", "--port", "3000",
		"--response-header", "NOEQUALS"), "--response-header must be KEY=VALUE")
	h.Require.Empty(sandboxTestCalls(m, "POST", sandboxTestPreviewsPath))
}

func Test_Sandbox_Preview_Update_SendsOnlySetFields(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRoute("PATCH", sandboxTestPreviewsPath+"/web", 200, sandboxTestPreview(false))

	// --private sends false, which differs from leaving access unchanged.
	h.Require.NoError(h.Execute("sandbox", "preview", "update", "--name", "sbx-1", "--preview-name", "web", "--private"))
	h.Require.Equal(map[string]any{"public": false}, sandboxTestCalls(m, "PATCH", sandboxTestPreviewsPath+"/web")[0].BodyJSON(t))

	h.Require.NoError(h.Execute("sandbox", "preview", "update", "--name", "sbx-1", "--preview-name", "web",
		"--public", "--request-header", "X-A=1", "--ttl", "1h"))
	h.Require.Equal(map[string]any{"public": true, "request_headers": map[string]any{"X-A": "1"}, "ttl": "1h0m0s"},
		sandboxTestCalls(m, "PATCH", sandboxTestPreviewsPath+"/web")[1].BodyJSON(t))

	// A passed 0 is sent for the server to judge, not dropped as unchanged.
	h.Require.NoError(h.Execute("sandbox", "preview", "update", "--name", "sbx-1", "--preview-name", "web", "--ttl", "0"))
	h.Require.Equal(map[string]any{"ttl": "0s"}, sandboxTestCalls(m, "PATCH", sandboxTestPreviewsPath+"/web")[2].BodyJSON(t))

	h.Require.ErrorContains(h.Execute("sandbox", "preview", "update", "--name", "sbx-1", "--preview-name", "web",
		"--public", "--private"), "cannot be combined")
	h.Require.Len(sandboxTestCalls(m, "PATCH", sandboxTestPreviewsPath+"/web"), 3)
}

func Test_Sandbox_Preview_Delete_ConfirmsAndLogsToStderr(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	deleted := sandboxTestPreview(false)
	deleted["status"] = "TERMINATED"
	m.SetRoute("DELETE", sandboxTestPreviewsPath+"/web", 200, deleted)

	// The harness stdin is not a terminal, so the prompt refuses.
	h.Require.ErrorContains(h.Execute("sandbox", "preview", "delete", "--name", "sbx-1", "--preview-name", "web"), "--yes")
	h.Require.Empty(sandboxTestCalls(m, "DELETE", sandboxTestPreviewsPath+"/web"))

	h.Require.NoError(h.Execute("sandbox", "preview", "delete", "--name", "sbx-1", "--preview-name", "web", "--yes"))
	h.Require.Empty(h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "Deleted preview web.")
}

func Test_Sandbox_Preview_Token_Create_PrintsURL(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRoute("POST", sandboxTestPreviewsPath+"/web/tokens", 201, map[string]any{
		"name": "t1", "token": "tok+1", "expires_at": sandboxTestTime, "expired": false,
	})
	m.SetRoute("GET", sandboxTestPreviewsPath+"/web", 200, sandboxTestPreview(false))

	h.Require.NoError(h.Execute("sandbox", "preview", "token", "create", "--name", "sbx-1", "--preview-name", "web",
		"--expires-at", "2026-11-01T12:00:00Z", "--if-not-exists"))
	h.Require.Equal(map[string]any{"expires_at": "2026-11-01T12:00:00Z", "create_if_not_exists": true},
		sandboxTestCalls(m, "POST", sandboxTestPreviewsPath+"/web/tokens")[0].BodyJSON(t))
	out := h.Stdout.String()
	h.Require.Contains(out, "Token:    tok+1\n")
	h.Require.Contains(out, "URL:      https://p.example?preview_token=tok%2B1\n")

	// JSON prints the token record alone, without getting the preview.
	h.Require.NoError(h.Execute("sandbox", "preview", "token", "create", "--name", "sbx-1", "--preview-name", "web",
		"--expires-at", "2026-11-01T12:00:00Z", "--output", "json"))
	h.Require.Len(sandboxTestCalls(m, "GET", sandboxTestPreviewsPath+"/web"), 1)
}

func Test_Sandbox_Preview_Token_Create_RequiresExpiresAt(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	h.Require.ErrorContains(h.Execute("sandbox", "preview", "token", "create", "--name", "sbx-1", "--preview-name", "web"), "expires-at")
	h.Require.Empty(sandboxTestCalls(m, "POST", sandboxTestPreviewsPath+"/web/tokens"))
}

func Test_Sandbox_Preview_Token_List_Table(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRoute("GET", sandboxTestPreviewsPath+"/web/tokens", 200, sandboxTestPage("",
		map[string]any{"name": "t1", "token": "secret", "expires_at": sandboxTestTime, "expired": true}))

	h.Require.NoError(h.Execute("sandbox", "preview", "token", "list", "--name", "sbx-1", "--preview-name", "web"))
	out := h.Stdout.String()
	h.Require.Regexp(`t1\s+2026-09-30T22:00:00Z\s+true`, out)
	// The token value is only in the JSON output.
	h.Require.NotContains(out, "secret")
}

func Test_Sandbox_Preview_Token_Delete_ConfirmsAndLogsToStderr(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRoute("DELETE", sandboxTestPreviewsPath+"/web/tokens/t1", 204, nil)

	h.Require.ErrorContains(h.Execute("sandbox", "preview", "token", "delete", "--name", "sbx-1", "--preview-name", "web",
		"--token-name", "t1"), "--yes")
	h.Require.Empty(sandboxTestCalls(m, "DELETE", sandboxTestPreviewsPath+"/web/tokens/t1"))

	h.Require.NoError(h.Execute("sandbox", "preview", "token", "delete", "--name", "sbx-1", "--preview-name", "web",
		"--token-name", "t1", "--yes"))
	h.Require.Len(sandboxTestCalls(m, "DELETE", sandboxTestPreviewsPath+"/web/tokens/t1"), 1)
	h.Require.Contains(h.Stderr.String(), "Deleted token t1.")
}
