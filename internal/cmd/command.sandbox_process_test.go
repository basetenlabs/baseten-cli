package cmd_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func Test_Sandbox_Process_Start_SendsBackgroundOptions(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRoute("GET", "/v1/sandboxes/instances/sbx-1", 200, sandboxTestRecord(m, "sbx-1", "DEPLOYED"))
	m.SetRoute("POST", "/process", 200, sandboxTestProcess("9", "running", 0))

	h.Require.NoError(h.Execute("sandbox", "process", "start", "--name", "sbx-1", "--process-name", "web",
		"--keep-alive", "--restart-on-failure", "--max-restarts", "3", "--wait-for-port", "8000",
		"--", "python", "-m", "http.server"))
	body := sandboxTestCalls(m, "POST", "/process")[0].BodyJSON(t)
	h.Require.Equal("python -m http.server", body["command"])
	h.Require.Equal("web", body["name"])
	h.Require.Equal(true, body["keepAlive"])
	h.Require.Equal(true, body["restartOnFailure"])
	h.Require.Equal(float64(3), body["maxRestarts"])
	h.Require.Equal([]any{float64(8000)}, body["waitForPorts"])
	h.Require.NotContains(body, "waitForCompletion")
	h.Require.Contains(h.Stdout.String(), "Started process 9 in sandbox sbx-1.")

	h.Require.ErrorContains(h.Execute("sandbox", "process", "start", "--name", "sbx-1", "--wait-for-port", "web", "--", "x"),
		"--wait-for-port")
}

func Test_Sandbox_Process_List_JSONIsAPIRecord(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRoute("GET", "/v1/sandboxes/instances/sbx-1", 200, sandboxTestRecord(m, "sbx-1", "DEPLOYED"))
	m.SetRoute("GET", "/process", 200, []any{sandboxTestProcess("9", "completed", 0), sandboxTestProcess("10", "running", 0)})

	h.Require.NoError(h.Execute("sandbox", "process", "list", "--name", "sbx-1"))
	h.Require.Contains(h.Stdout.String(), "proc-10")
	h.Require.Contains(h.Stdout.String(), "running")

	h.Require.NoError(h.Execute("sandbox", "process", "list", "--name", "sbx-1", "--output", "json"))
	var listed struct {
		Items []map[string]any `json:"items"`
	}
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &listed))
	h.Require.Len(listed.Items, 2)
	h.Require.Equal("9", listed.Items[0]["pid"])
	h.Require.Equal("2026-09-30T22:00:01Z", listed.Items[0]["startedAt"])
}

func Test_Sandbox_Process_Describe_ByName(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRoute("GET", "/v1/sandboxes/instances/sbx-1", 200, sandboxTestRecord(m, "sbx-1", "DEPLOYED"))
	m.SetRoute("GET", "/process/web", 200, sandboxTestProcess("9", "completed", 0))

	h.Require.NoError(h.Execute("sandbox", "process", "describe", "--name", "sbx-1", "--process-name", "web"))
	out := h.Stdout.String()
	h.Require.Contains(out, "PID:          9")
	h.Require.Contains(out, "Exit code:    0")
	// The captured output, "hi", is not printed.
	h.Require.NotContains(out, "\nhi\n")

	h.Require.ErrorContains(h.Execute("sandbox", "process", "describe", "--name", "sbx-1"), "pid")
}

func Test_Sandbox_Process_Logs_SnapshotAndTail(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRoute("GET", "/v1/sandboxes/instances/sbx-1", 200, sandboxTestRecord(m, "sbx-1", "DEPLOYED"))
	m.SetRoute("GET", "/process/9/logs", 200, map[string]any{"stdout": "a\nc\n", "stderr": "b\n", "logs": "a\nb\nc"})
	m.SetRouteFunc("GET", "/process/9/logs/stream", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "stdout:a\nstderr:b\nstdout:c\n")
	})

	h.Require.NoError(h.Execute("sandbox", "process", "logs", "--name", "sbx-1", "--pid", "9"))
	h.Require.Equal("a\nb\nc\n", h.Stdout.String())

	h.Require.NoError(h.Execute("sandbox", "process", "logs", "--name", "sbx-1", "--pid", "9", "--output", "jsonl"))
	// The output so far does not say which stream a line came from.
	h.Require.Equal(`{"text":"a"}`+"\n"+`{"text":"b"}`+"\n"+`{"text":"c"}`+"\n", h.Stdout.String())

	h.Require.NoError(h.Execute("sandbox", "process", "logs", "--name", "sbx-1", "--pid", "9", "--tail"))
	h.Require.Equal("a\nc\n", h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "b\n")

	h.Require.NoError(h.Execute("sandbox", "process", "logs", "--name", "sbx-1", "--pid", "9", "--tail", "--output", "jsonl"))
	h.Require.Equal(strings.Join([]string{
		`{"stream":"stdout","text":"a"}`, `{"stream":"stderr","text":"b"}`, `{"stream":"stdout","text":"c"}`,
	}, "\n")+"\n", h.Stdout.String())
}

func Test_Sandbox_Process_Wait_PassesExitCode(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRoute("GET", "/v1/sandboxes/instances/sbx-1", 200, sandboxTestRecord(m, "sbx-1", "DEPLOYED"))
	m.SetRoute("GET", "/process/9", 200, sandboxTestProcess("9", "failed", 4))

	err := h.Execute("sandbox", "process", "wait", "--name", "sbx-1", "--pid", "9")
	h.Require.ErrorContains(err, "code 4")
	h.Require.Equal(4, h.ExitCode)
	h.Require.Contains(h.Stdout.String(), "Status:       failed")
}

func Test_Sandbox_Process_StopAndKill(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRoute("GET", "/v1/sandboxes/instances/sbx-1", 200, sandboxTestRecord(m, "sbx-1", "DEPLOYED"))
	m.SetRoute("DELETE", "/process/9", 200, map[string]any{"message": "ok"})
	m.SetRoute("DELETE", "/process/9/kill", 200, map[string]any{"message": "ok"})

	h.Require.NoError(h.Execute("sandbox", "process", "stop", "--name", "sbx-1", "--pid", "9"))
	h.Require.Len(sandboxTestCalls(m, "DELETE", "/process/9"), 1)
	h.Require.Empty(h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "Requested process 9 in sandbox sbx-1 to stop.")

	h.Require.NoError(h.Execute("sandbox", "process", "kill", "--name", "sbx-1", "--pid", "9"))
	h.Require.Len(sandboxTestCalls(m, "DELETE", "/process/9/kill"), 1)
}
