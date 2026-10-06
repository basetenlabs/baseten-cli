package cmd_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
)

// sandboxTestTime is what every fixture record uses, so output assertions
// never compute a timestamp.
var sandboxTestTime = time.Date(2026, 9, 30, 22, 0, 0, 0, time.UTC)

// newSandboxTestAPI returns the mock management API with the token exchange
// served. The mock also serves a sandbox's own API for records from
// sandboxTestRecord, whose URL is the mock's.
func newSandboxTestAPI(h *CommandHarness) *MockManagementAPI {
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/token", 200, map[string]any{
		"token":      "tok-1",
		"expires_at": sandboxTestTime.Add(time.Hour).Format(time.RFC3339),
	})
	return m
}

// sandboxTestRecord is a control plane sandbox record, with its URL pointed at
// the mock so its own API is served there too.
func sandboxTestRecord(m *MockManagementAPI, name, status string) map[string]any {
	return map[string]any{
		"name": name, "status": status, "url": m.URL,
		"image": "baseten/base-image:latest", "memory": 4096, "region": "us-was-1",
		"labels":     map[string]string{"b": "2", "a": "1"},
		"envs":       []any{map[string]any{"name": "TOKEN", "value": "****", "secret": true}},
		"created_at": sandboxTestTime,
	}
}

// sandboxTestProcess is a sandbox API process record.
func sandboxTestProcess(pid, status string, exitCode int) map[string]any {
	return map[string]any{
		"pid": pid, "name": "proc-" + pid, "command": "echo hi", "status": status,
		"exitCode": exitCode, "stdout": "hi\n", "stderr": "", "logs": "hi\n", "workingDir": "/",
		"startedAt": "2026-09-30T22:00:01Z", "completedAt": "2026-09-30T22:00:02Z",
	}
}

// sandboxTestCalls returns the recorded calls to method and path.
func sandboxTestCalls(m *MockManagementAPI, method, path string) []MockAPICall {
	var calls []MockAPICall
	for _, call := range m.Calls() {
		if call.Method == method && call.Path == path {
			calls = append(calls, call)
		}
	}
	return calls
}

// sandboxTestPage is one page of a sandbox API listing.
func sandboxTestPage(cursor string, items ...any) map[string]any {
	pagination := map[string]any{"has_more": cursor != ""}
	if cursor != "" {
		pagination["cursor"] = cursor
	}
	return map[string]any{"items": items, "pagination": pagination}
}

func Test_Sandbox_List_FollowsPages(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRouteFunc("GET", "/v1/sandboxes/instances", func(w http.ResponseWriter, r *http.Request) {
		page := sandboxTestPage("c1", sandboxTestRecord(m, "sbx-1", "DEPLOYED"))
		if r.URL.Query().Get("cursor") == "c1" {
			page = sandboxTestPage("", sandboxTestRecord(m, "sbx-2", "DEPLOYING"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(page)
	})

	h.Require.NoError(h.Execute("sandbox", "list", "--limit", "0"))
	calls := sandboxTestCalls(m, "GET", "/v1/sandboxes/instances")
	h.Require.Len(calls, 2)
	h.Require.Equal("100", calls[0].Query().Get("limit"))
	h.Require.Empty(calls[0].Query().Get("team_id"))
	h.Require.Equal("c1", calls[1].Query().Get("cursor"))
	h.Require.Contains(h.Stdout.String(), "sbx-1")
	h.Require.Contains(h.Stdout.String(), "sbx-2")
	h.Require.Contains(h.Stdout.String(), "DEPLOYING")

	h.Require.NoError(h.Execute("sandbox", "list", "--limit", "0", "--output", "json"))
	var listed struct {
		Items []map[string]any `json:"items"`
	}
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &listed))
	h.Require.Len(listed.Items, 2)
	// The API's own record.
	h.Require.Equal(m.URL, listed.Items[0]["url"])
}

func Test_Sandbox_List_Limit(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRoute("GET", "/v1/sandboxes/instances", 200, sandboxTestPage("c1", sandboxTestRecord(m, "sbx-1", "DEPLOYED")))

	h.Require.NoError(h.Execute("sandbox", "list", "--limit", "1"))
	calls := sandboxTestCalls(m, "GET", "/v1/sandboxes/instances")
	h.Require.Len(calls, 1)
	h.Require.Equal("1", calls[0].Query().Get("limit"))
	h.Require.Contains(h.Stderr.String(), "Reached the --limit of 1")
}

func Test_Sandbox_List_RepeatedCursorFails(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRoute("GET", "/v1/sandboxes/instances", 200, sandboxTestPage("same", sandboxTestRecord(m, "sbx-1", "DEPLOYED")))

	h.Require.ErrorContains(h.Execute("sandbox", "list", "--limit", "0"), "cursor")
}

func Test_Sandbox_List_StatusAndTeam(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRoute("GET", "/v1/teams", 200, map[string]any{"teams": []any{map[string]any{"id": "team-9", "name": "nine"}}})
	m.SetRoute("GET", "/v1/sandboxes/instances", 200, sandboxTestPage(""))

	h.Require.NoError(h.Execute("sandbox", "list", "--team", "nine", "--status", "deployed", "--status", "failed"))
	calls := sandboxTestCalls(m, "GET", "/v1/sandboxes/instances")
	h.Require.Len(calls, 1)
	h.Require.Equal("team-9", calls[0].Query().Get("team_id"))
	h.Require.Equal([]string{"DEPLOYED", "FAILED"}, calls[0].Query()["status"])
	h.Require.Contains(h.Stderr.String(), "No sandboxes found.")
}

func Test_Sandbox_Create_SendsOnlySetFields(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRoute("POST", "/v1/sandboxes/instances", 201, sandboxTestRecord(m, "sbx-1", "DEPLOYED"))

	h.Require.NoError(h.Execute("sandbox", "create"))
	h.Require.Equal(map[string]any{}, sandboxTestCalls(m, "POST", "/v1/sandboxes/instances")[0].BodyJSON(t))

	h.Require.NoError(h.Execute("sandbox", "create", "--name", "sbx-1", "--if-not-exists",
		"--region", "us-was-1", "--memory", "8192", "--image", "img:latest",
		"--env", "TOKEN=s3cret", "--plain-env", "WORKERS=4", "--label", "team=cli"))
	body := sandboxTestCalls(m, "POST", "/v1/sandboxes/instances")[1].BodyJSON(t)
	h.Require.Equal(map[string]any{
		"name": "sbx-1", "create_if_not_exists": true, "region": "us-was-1", "memory": float64(8192),
		"image": "img:latest", "labels": map[string]any{"team": "cli"},
		// --env leaves secret to the server's default.
		"envs": []any{
			map[string]any{"name": "TOKEN", "value": "s3cret"},
			map[string]any{"name": "WORKERS", "value": "4", "secret": false},
		},
	}, body)
	h.Require.Contains(h.Stdout.String(), "Name:         sbx-1")
	h.Require.Contains(h.Stdout.String(), "Labels:       a=1, b=2")
}

func Test_Sandbox_Create_RejectsMalformedKeyValues(t *testing.T) {
	h := NewCommandHarness(t)
	h.Require.ErrorContains(h.Execute("sandbox", "create", "--env", "NOEQUALS"), "KEY=VALUE")
	h.Require.ErrorContains(h.Execute("sandbox", "create", "--label", "=value"), "KEY=VALUE")
	h.Require.ErrorContains(h.Execute("sandbox", "create", "--env", "A=1", "--plain-env", "A=2"), "both")
}

func Test_Sandbox_Describe_ShowSecretsAndText(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRoute("GET", "/v1/sandboxes/instances/sbx-1", 200, sandboxTestRecord(m, "sbx-1", "DEPLOYED"))

	h.Require.NoError(h.Execute("sandbox", "describe", "--name", "sbx-1"))
	h.Require.Empty(sandboxTestCalls(m, "GET", "/v1/sandboxes/instances/sbx-1")[0].Query().Get("show_secrets"))
	out := h.Stdout.String()
	h.Require.Contains(out, "Status:       DEPLOYED")
	h.Require.Contains(out, "Memory:       4096 MB")
	h.Require.Contains(out, "Envs:         TOKEN=****")
	// Empty fields are left out.
	h.Require.NotContains(out, "External ID")

	h.Require.NoError(h.Execute("sandbox", "describe", "--name", "sbx-1", "--show-secrets"))
	h.Require.Equal("true", sandboxTestCalls(m, "GET", "/v1/sandboxes/instances/sbx-1")[1].Query().Get("show_secrets"))
}

func Test_Sandbox_Update_SendsOnlySetFields(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRoute("PATCH", "/v1/sandboxes/instances/sbx-1", 200, sandboxTestRecord(m, "sbx-1", "DEPLOYED"))

	h.Require.NoError(h.Execute("sandbox", "update", "--name", "sbx-1", "--label", "env=dev"))
	// Without --env, environment variables are left alone, not cleared.
	h.Require.Equal(map[string]any{"labels": map[string]any{"env": "dev"}},
		sandboxTestCalls(m, "PATCH", "/v1/sandboxes/instances/sbx-1")[0].BodyJSON(t))
}

func Test_Sandbox_Delete_ConfirmsAndLogsToStderr(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRoute("DELETE", "/v1/sandboxes/instances/sbx-1", 202, sandboxTestRecord(m, "sbx-1", "DELETING"))

	// The harness stdin is not a terminal, so the prompt refuses.
	h.Require.ErrorContains(h.Execute("sandbox", "delete", "--name", "sbx-1"), "--yes")
	h.Require.Empty(sandboxTestCalls(m, "DELETE", "/v1/sandboxes/instances/sbx-1"))

	h.Require.NoError(h.Execute("sandbox", "delete", "--name", "sbx-1", "--yes"))
	h.Require.Empty(h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "Deleting sandbox sbx-1.")
}

func Test_Sandbox_Exec_StreamsAndPassesExitCode(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRoute("GET", "/v1/sandboxes/instances/sbx-1", 200, sandboxTestRecord(m, "sbx-1", "DEPLOYED"))
	result, err := json.Marshal(sandboxTestProcess("9", "completed", 7))
	h.Require.NoError(err)
	resultEvent, err := json.Marshal(map[string]string{"type": "result", "data": string(result)})
	h.Require.NoError(err)
	m.SetRouteFunc("POST", "/process", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = io.WriteString(w, `{"type":"stdout","data":"hello "}`+"\n"+
			`{"type":"stderr","data":"oops\n"}`+"\n"+
			`{"type":"stdout","data":"world\n"}`+"\n"+string(resultEvent)+"\n")
	})

	err = h.Execute("sandbox", "exec", "--name", "sbx-1", "--working-dir", "/app", "--env", "A=1", "--timeout", "90s",
		"--process-name", "job", "--", "make", "test")
	h.Require.ErrorContains(err, "code 7")
	h.Require.Equal(7, h.ExitCode)
	h.Require.Equal("hello world\n", h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "oops\n")
	body := sandboxTestCalls(m, "POST", "/process")[0].BodyJSON(t)
	h.Require.Equal("make test", body["command"])
	h.Require.Equal("/app", body["workingDir"])
	h.Require.Equal("job", body["name"])
	h.Require.Equal(float64(90), body["timeout"])
	h.Require.Equal(map[string]any{"A": "1"}, body["env"])
}

func Test_Sandbox_Exec_JSONPrintsFinalRecord(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRoute("GET", "/v1/sandboxes/instances/sbx-1", 200, sandboxTestRecord(m, "sbx-1", "DEPLOYED"))
	m.SetRoute("POST", "/process", 200, sandboxTestProcess("9", "completed", 3))

	err := h.Execute("sandbox", "exec", "--name", "sbx-1", "--output", "json", "--", "false")
	h.Require.Error(err)
	h.Require.Equal(3, h.ExitCode)
	h.Require.Equal(true, sandboxTestCalls(m, "POST", "/process")[0].BodyJSON(t)["waitForCompletion"])
	// The record reports the failure itself, so no error envelope follows it.
	var record map[string]any
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &record))
	h.Require.Equal(float64(3), record["exitCode"])
	h.Require.Equal("hi\n", record["stdout"])
}

func Test_Sandbox_Exec_QuotesArguments(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRoute("GET", "/v1/sandboxes/instances/sbx-1", 200, sandboxTestRecord(m, "sbx-1", "DEPLOYED"))
	m.SetRoute("POST", "/process", 200, sandboxTestProcess("9", "completed", 0))

	h.Require.NoError(h.Execute("sandbox", "exec", "--name", "sbx-1", "-o", "json", "--", "printf", `%s\n`, "it's here", ""))
	h.Require.Equal(`printf '%s\n' 'it'\''s here' ''`, sandboxTestCalls(m, "POST", "/process")[0].BodyJSON(t)["command"])
	// One argument is the whole command line, for the sandbox's shell.
	h.Require.NoError(h.Execute("sandbox", "exec", "--name", "sbx-1", "-o", "json", "--", "echo $HOME | wc -c"))
	h.Require.Equal("echo $HOME | wc -c", sandboxTestCalls(m, "POST", "/process")[1].BodyJSON(t)["command"])
	h.Require.ErrorContains(h.Execute("sandbox", "exec", "--name", "sbx-1"), "a command is required")
}

func Test_Sandbox_Connect_RequiresTerminalBeforeConnecting(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)

	// The harness's stdin and stdout are buffers, not a terminal.
	h.Require.ErrorContains(h.Execute("sandbox", "connect", "--name", "sbx-1"), "interactive terminal")
	h.Require.Equal(int(cmd.ExitUsage), h.ExitCode)
	h.Require.Empty(m.Calls())
}

func Test_Sandbox_Exec_StdinStartsWritesAndWaits(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRoute("GET", "/v1/sandboxes/instances/sbx-1", 200, sandboxTestRecord(m, "sbx-1", "DEPLOYED"))
	m.SetRoute("POST", "/process", 200, sandboxTestProcess("9", "running", 0))
	m.SetRoute("POST", "/process/9/stdin", 200, map[string]any{"message": "ok"})
	// Like wc, the process writes only once its stdin is closed.
	stdinClosed := make(chan struct{})
	m.SetRouteFunc("DELETE", "/process/9/stdin", func(w http.ResponseWriter, _ *http.Request) {
		close(stdinClosed)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"message":"ok"}`)
	})
	m.SetRouteFunc("GET", "/process/9/logs/stream", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-stdinClosed:
			_, _ = io.WriteString(w, "stdout:2 lines\n")
		case <-r.Context().Done():
		}
	})
	m.SetRoute("GET", "/process/9", 200, sandboxTestProcess("9", "completed", 0))
	h.Stdin.WriteString("a\nb\n")

	h.Require.NoError(h.Execute("sandbox", "exec", "--name", "sbx-1", "--stdin", "--", "wc", "-l"))
	h.Require.Equal(true, sandboxTestCalls(m, "POST", "/process")[0].BodyJSON(t)["stdin"])
	writes := sandboxTestCalls(m, "POST", "/process/9/stdin")
	var written strings.Builder
	for _, write := range writes {
		written.WriteString(write.Body)
	}
	h.Require.Equal("a\nb\n", written.String())
	h.Require.Len(sandboxTestCalls(m, "DELETE", "/process/9/stdin"), 1)
	h.Require.Equal("2 lines\n", h.Stdout.String())
}

func Test_Sandbox_Exec_StdinFailureWhileRunningEndsFollowing(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRoute("GET", "/v1/sandboxes/instances/sbx-1", 200, sandboxTestRecord(m, "sbx-1", "DEPLOYED"))
	m.SetRoute("POST", "/process", 200, sandboxTestProcess("9", "running", 0))
	m.SetRoute("POST", "/process/9/stdin", 500, map[string]any{"error": "write failed"})
	// The process waits for the rest of its input, so its output never ends.
	m.SetRouteFunc("GET", "/process/9/logs/stream", func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	m.SetRoute("GET", "/process/9", 200, sandboxTestProcess("9", "running", 0))
	h.Stdin.WriteString("a\n")

	err := h.Execute("sandbox", "exec", "--name", "sbx-1", "--stdin", "--", "wc", "-l")
	h.Require.ErrorContains(err, "writing stdin to sandbox sbx-1")
	h.Require.ErrorContains(err, "write failed")
	// Its stdin is left open, since the input is incomplete.
	h.Require.Empty(sandboxTestCalls(m, "DELETE", "/process/9/stdin"))
}

func Test_Sandbox_Exec_StdinFailureWhileRunningEndsWaiting(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRoute("GET", "/v1/sandboxes/instances/sbx-1", 200, sandboxTestRecord(m, "sbx-1", "DEPLOYED"))
	m.SetRoute("POST", "/process", 200, sandboxTestProcess("9", "running", 0))
	m.SetRoute("POST", "/process/9/stdin", 500, map[string]any{"error": "write failed"})
	// The process waits for the rest of its input, so it never ends.
	m.SetRoute("GET", "/process/9", 200, sandboxTestProcess("9", "running", 0))
	h.Stdin.WriteString("a\n")

	err := h.Execute("sandbox", "exec", "--name", "sbx-1", "--stdin", "--output", "json", "--", "wc", "-l")
	h.Require.ErrorContains(err, "writing stdin to sandbox sbx-1")
	h.Require.ErrorContains(err, "write failed")
}

func Test_Sandbox_Exec_StdinFailureAfterExitIsIgnored(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRoute("GET", "/v1/sandboxes/instances/sbx-1", 200, sandboxTestRecord(m, "sbx-1", "DEPLOYED"))
	m.SetRoute("POST", "/process", 200, sandboxTestProcess("9", "running", 0))
	// Like head, the process exits without reading all of its input.
	m.SetRoute("POST", "/process/9/stdin", 409, map[string]any{"error": "process is not running"})
	m.SetRouteFunc("GET", "/process/9/logs/stream", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "stdout:first\n")
	})
	m.SetRoute("GET", "/process/9", 200, sandboxTestProcess("9", "completed", 2))
	h.Stdin.WriteString("a\n")

	err := h.Execute("sandbox", "exec", "--name", "sbx-1", "--stdin", "--", "head", "-c", "1")
	// The process's own exit code, not the copy's failure.
	h.Require.ErrorContains(err, "code 2")
	h.Require.NotContains(err.Error(), "writing stdin")
	h.Require.Equal(2, h.ExitCode)
	h.Require.Equal("first\n", h.Stdout.String())
}
