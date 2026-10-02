package cmd_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
)

// sandboxTestTime is what every fixture record uses, so output assertions
// never compute a timestamp.
var sandboxTestTime = time.Date(2026, 9, 30, 22, 0, 0, 0, time.UTC)

// sandboxRecord is the control-plane record the mock serves, with its URL
// pointed at execURL when given.
func sandboxRecord(name, status, execURL string) map[string]any {
	record := map[string]any{
		"name": name, "status": status, "state": "RUNNING",
		"image": "baseten/base-image:latest", "memory": 4096,
		"region": "us-was-1", "enabled": true,
		"labels":     map[string]string{"k": "v"},
		"created_at": sandboxTestTime, "created_by": "u1",
	}
	if execURL != "" {
		record["url"] = execURL
	}
	return record
}

// sandboxTestTokenRoute serves the SDK's token exchange.
func sandboxTestTokenRoute(m *MockManagementAPI) {
	m.SetRouteFunc("POST", "/v1/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "tok-1",
			"expires_at": sandboxTestTime.Add(time.Hour).Format(time.RFC3339),
		})
	})
}

func sandboxCallsFor(m *MockManagementAPI, method, path string) []MockAPICall {
	var calls []MockAPICall
	for _, call := range m.Calls() {
		if call.Method == method && call.Path == path {
			calls = append(calls, call)
		}
	}
	return calls
}

func Test_Sandbox_Create_ReturnsImmediately_SendsOnlySetFields(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	sandboxTestTokenRoute(m)
	m.SetRoute("POST", "/v1/sandboxes/instances", 201, sandboxRecord("sbx-1", "DEPLOYING", "https://sbx-1.invalid"))

	h.Require.NoError(h.Execute("sandbox", "create",
		"--name", "sbx-1", "--region", "us-was-1", "--env", "A=1", "--label", "k=v",
		"--memory-mb", "8192", "--image", "img:tag"))

	calls := sandboxCallsFor(m, "POST", "/v1/sandboxes/instances")
	h.Require.Len(calls, 1)
	body := calls[0].BodyJSON(t)
	h.Require.Equal("sbx-1", body["name"])
	h.Require.Equal("us-was-1", body["region"])
	h.Require.Equal(float64(8192), body["memory"])
	h.Require.Equal("img:tag", body["image"])
	envs := body["envs"].([]any)
	h.Require.Len(envs, 1)
	h.Require.Equal(map[string]any{"name": "A", "value": "1", "secret": true}, envs[0])
	h.Require.Equal(map[string]any{"k": "v"}, body["labels"])
	h.Require.NotContains(body, "display_name")

	// Without --wait there is no polling and the record prints as of creation.
	h.Require.Empty(sandboxCallsFor(m, "GET", "/v1/sandboxes/instances/sbx-1"))
	h.Require.Contains(h.Stdout.String(), "Name:        sbx-1")
	h.Require.Contains(h.Stdout.String(), "Status:      DEPLOYING")
	h.Require.Contains(h.Stdout.String(), "URL:         https://sbx-1.invalid")
}

func Test_Sandbox_Create_WaitsUntilDeployed(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	sandboxTestTokenRoute(m)
	describeCalls := 0
	m.SetRoute("POST", "/v1/sandboxes/instances", 201, sandboxRecord("sbx-1", "DEPLOYING", "https://sbx-1.invalid"))
	m.SetRouteFunc("GET", "/v1/sandboxes/instances/sbx-1", func(w http.ResponseWriter, r *http.Request) {
		describeCalls++
		status := "DEPLOYING"
		if describeCalls > 1 {
			status = "DEPLOYED"
		}
		record := sandboxRecord("sbx-1", status, "https://sbx-1.invalid")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(record)
	})

	h.Require.NoError(h.Execute("sandbox", "create", "--name", "sbx-1", "--wait"))

	h.Require.Contains(h.Stderr.String(), "Waiting for sandbox sbx-1 to deploy")
	h.Require.Contains(h.Stdout.String(), "Status:      DEPLOYED")
	h.Require.Contains(h.Stdout.String(), "URL:         https://sbx-1.invalid")
}

func Test_Sandbox_Create_FailedDeployErrors(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	sandboxTestTokenRoute(m)
	m.SetRoute("POST", "/v1/sandboxes/instances", 201, sandboxRecord("sbx-1", "DEPLOYING", "https://sbx-1.invalid"))
	m.SetRoute("GET", "/v1/sandboxes/instances/sbx-1", 200, sandboxRecord("sbx-1", "FAILED", ""))

	err := h.Execute("sandbox", "create", "--name", "sbx-1", "--wait")
	h.Require.ErrorContains(err, "failed to deploy")
}

func Test_Sandbox_List_TableAndJSON(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	sandboxTestTokenRoute(m)
	m.SetRouteFunc("GET", "/v1/sandboxes/instances", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items":      []any{sandboxRecord("sbx-1", "DEPLOYED", "https://sbx-1.invalid")},
				"pagination": map[string]any{"has_more": true, "cursor": "c2"},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items":      []any{sandboxRecord("sbx-2", "DEPLOYING", "")},
			"pagination": map[string]any{"has_more": false, "cursor": nil},
		})
	})

	h.Require.NoError(h.Execute("sandbox", "list"))
	h.Require.Contains(h.Stdout.String(), "NAME")
	h.Require.Contains(h.Stdout.String(), "sbx-1")
	h.Require.Contains(h.Stdout.String(), "sbx-2")
	// Both pages came through one table.
	h.Require.Contains(h.Stdout.String(), "DEPLOYED")
	h.Require.Contains(h.Stdout.String(), "DEPLOYING")

	h.Require.NoError(h.Execute("sandbox", "list", "--output", "json"))
	var listed struct {
		Items []map[string]any `json:"items"`
	}
	h.Require.NoError(json.Unmarshal([]byte(h.Stdout.String()), &listed))
	h.Require.Len(listed.Items, 2)
}

func Test_Sandbox_List_Empty(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	sandboxTestTokenRoute(m)
	m.SetRouteFunc("GET", "/v1/sandboxes/instances", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []any{}, "pagination": map[string]any{"has_more": false},
		})
	})

	h.Require.NoError(h.Execute("sandbox", "list"))
	h.Require.Contains(h.Stderr.String(), "No sandboxes found.")
}

func Test_Sandbox_Describe(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	sandboxTestTokenRoute(m)
	m.SetRoute("GET", "/v1/sandboxes/instances/sbx-1", 200, sandboxRecord("sbx-1", "DEPLOYED", "https://sbx-1.invalid"))

	h.Require.NoError(h.Execute("sandbox", "describe", "--name", "sbx-1"))
	h.Require.Contains(h.Stdout.String(), "Name:        sbx-1")
	h.Require.Contains(h.Stdout.String(), "Memory:      4096 MB")
	h.Require.Contains(h.Stdout.String(), "Labels:      k=v")
	h.Require.Contains(h.Stdout.String(), "Enabled:     true")
}

func Test_Sandbox_Update_SendsOnlySetFields(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	sandboxTestTokenRoute(m)
	m.SetRoute("PATCH", "/v1/sandboxes/instances/sbx-1", 200, sandboxRecord("sbx-1", "DEPLOYED", "https://sbx-1.invalid"))

	h.Require.NoError(h.Execute("sandbox", "update", "--name", "sbx-1", "--label", "a=b"))

	calls := sandboxCallsFor(m, "PATCH", "/v1/sandboxes/instances/sbx-1")
	h.Require.Len(calls, 1)
	body := calls[0].BodyJSON(t)
	h.Require.Equal(map[string]any{"a": "b"}, body["labels"])
	h.Require.NotContains(body, "envs")
	h.Require.NotContains(body, "enabled")
	h.Require.NotContains(body, "image")
}

func Test_Sandbox_CreateIfNotExistsAndDescribeShowSecrets(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	sandboxTestTokenRoute(m)
	m.SetRoute("POST", "/v1/sandboxes/instances", 201, sandboxRecord("sbx-1", "DEPLOYING", "https://sbx-1.invalid"))
	m.SetRoute("GET", "/v1/sandboxes/instances/sbx-1", 200, sandboxRecord("sbx-1", "DEPLOYED", "https://sbx-1.invalid"))

	h.Require.NoError(h.Execute("sandbox", "create", "--name", "sbx-1", "--or-get-existing"))
	calls := sandboxCallsFor(m, "POST", "/v1/sandboxes/instances")
	h.Require.Len(calls, 1)
	h.Require.Equal(true, calls[0].BodyJSON(t)["create_if_not_exists"])

	// Unset stays off the wire.
	h.Require.NoError(h.Execute("sandbox", "create", "--name", "sbx-1"))
	calls = sandboxCallsFor(m, "POST", "/v1/sandboxes/instances")
	body := calls[len(calls)-1].BodyJSON(t)
	if _, present := body["create_if_not_exists"]; present {
		h.Require.Fail("unset create_if_not_exists must be omitted")
	}

	h.Require.NoError(h.Execute("sandbox", "describe", "--name", "sbx-1", "--show-secrets"))
	describe := sandboxCallsFor(m, "GET", "/v1/sandboxes/instances/sbx-1")
	h.Require.Equal("true", describe[len(describe)-1].Query().Get("show_secrets"))
}

func Test_Sandbox_Delete_NeedsConfirmationWithoutTty(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	sandboxTestTokenRoute(m)
	m.SetRoute("DELETE", "/v1/sandboxes/instances/sbx-1", 202, sandboxRecord("sbx-1", "DELETING", ""))

	// The harness stdin is a buffer, not a terminal, so the prompt refuses.
	err := h.Execute("sandbox", "delete", "--name", "sbx-1")
	h.Require.ErrorContains(err, "--yes")
	h.Require.Empty(sandboxCallsFor(m, "DELETE", "/v1/sandboxes/instances/sbx-1"))

	h.Require.NoError(h.Execute("sandbox", "delete", "--name", "sbx-1", "--yes"))
	h.Require.Len(sandboxCallsFor(m, "DELETE", "/v1/sandboxes/instances/sbx-1"), 1)
	h.Require.Contains(h.Stderr.String(), "Deleting sandbox sbx-1")
}

func sandboxExecStreamServer(t *testing.T, lines ...string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("streaming requires the event-stream accept, got %q", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		for _, line := range lines {
			_, _ = io.WriteString(w, line+"\n")
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func Test_Sandbox_Exec_StreamsAndPassesExitCode(t *testing.T) {
	resultRecord, err := json.Marshal(map[string]any{
		"command": "false", "name": "false", "pid": "9", "status": "completed",
		"exitCode": 7, "stdout": "hello\n", "stderr": "to stderr\n",
		"logs": "hello\n", "workingDir": "/", "startedAt": "2026-09-30T22:00:01Z",
		"completedAt": "2026-09-30T22:00:02Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	resultEvent, err := json.Marshal(map[string]string{"type": "result", "data": string(resultRecord)})
	if err != nil {
		t.Fatal(err)
	}
	execServer := sandboxExecStreamServer(t,
		`{"type":"stdout","data":"hello"}`,
		`{"type":"stderr","data":"to stderr"}`,
		string(resultEvent),
	)
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	sandboxTestTokenRoute(m)
	m.SetRoute("GET", "/v1/sandboxes/instances/sbx-1", 200, sandboxRecord("sbx-1", "DEPLOYED", execServer.URL))

	// The harness reports a non-zero command exit as an error; the point of
	// the test is that the command's exit code becomes the CLI's.
	err = h.Execute("sandbox", "exec", "--name", "sbx-1", "--", "false")
	h.Require.ErrorContains(err, "code 7")
	h.Require.Equal(7, h.ExitCode)
	h.Require.Contains(h.Stdout.String(), "hello")
	h.Require.Contains(h.Stderr.String(), "to stderr")
}

func Test_Sandbox_Exec_JSONMode(t *testing.T) {
	execServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"command":"echo hi","name":"echo","pid":"9","status":"completed","exitCode":0,"stdout":"hi\n","stderr":"","logs":"hi\n","workingDir":"/","startedAt":"2026-09-30T22:00:01Z","completedAt":"2026-09-30T22:00:02Z"}`)
	}))
	t.Cleanup(execServer.Close)
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	sandboxTestTokenRoute(m)
	m.SetRoute("GET", "/v1/sandboxes/instances/sbx-1", 200, sandboxRecord("sbx-1", "DEPLOYED", execServer.URL))

	h.Require.NoError(h.Execute("sandbox", "exec", "--name", "sbx-1", "--output", "json", "--", "echo", "hi"))
	var executed map[string]any
	h.Require.NoError(json.Unmarshal([]byte(h.Stdout.String()), &executed))
	h.Require.Equal(float64(0), executed["exit_code"])
	h.Require.Equal("hi\n", executed["stdout"])
	h.Require.Equal(0, h.ExitCode)
}

func Test_Sandbox_Exec_RequiresDeployed(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	sandboxTestTokenRoute(m)
	m.SetRoute("GET", "/v1/sandboxes/instances/sbx-1", 200, sandboxRecord("sbx-1", "DEPLOYING", ""))

	err := h.Execute("sandbox", "exec", "--name", "sbx-1", "--", "true")
	h.Require.ErrorContains(err, "not DEPLOYED")
}

func Test_Sandbox_EnvAndLabelMustBeKeyValue(t *testing.T) {
	h := NewCommandHarness(t)

	err := h.Execute("sandbox", "create", "--name", "sbx-1", "--env", "NOEQUALS")
	h.Require.ErrorContains(err, "KEY=VALUE")

	err = h.Execute("sandbox", "create", "--name", "sbx-1", "--label", "=novalue")
	h.Require.ErrorContains(err, "KEY=VALUE")
}

func Test_Sandbox_TeamFlagResolvesNameToID(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	sandboxTestTokenRoute(m)
	m.SetRoute("GET", "/v1/teams", 200, map[string]any{
		"teams": []any{map[string]any{"id": "team-9", "name": "nine"}},
	})
	m.SetRouteFunc("GET", "/v1/sandboxes/instances", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []any{}, "pagination": map[string]any{"has_more": false},
		})
	})

	h.Require.NoError(h.Execute("sandbox", "list", "--team", "nine"))
	calls := sandboxCallsFor(m, "GET", "/v1/sandboxes/instances")
	h.Require.Len(calls, 1)
	h.Require.Equal("team-9", calls[0].Query().Get("team_id"))
}

func Test_Sandbox_Exec_QuotesArguments(t *testing.T) {
	var receivedCommand string
	execServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		_ = json.NewDecoder(r.Body).Decode(&request)
		receivedCommand = request["command"].(string)
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = io.WriteString(w, `{"type":"result","data":"{\"command\":\"x\",\"name\":\"x\",\"pid\":\"9\",\"status\":\"completed\",\"exitCode\":0,\"stdout\":\"\",\"stderr\":\"\",\"logs\":\"\",\"workingDir\":\"/\",\"startedAt\":\"2026-09-30T22:00:01Z\",\"completedAt\":\"2026-09-30T22:00:02Z\"}"}
`)
	}))
	t.Cleanup(execServer.Close)
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	sandboxTestTokenRoute(m)
	m.SetRoute("GET", "/v1/sandboxes/instances/sbx-1", 200, sandboxRecord("sbx-1", "DEPLOYED", execServer.URL))

	h.Require.NoError(h.Execute("sandbox", "exec", "--name", "sbx-1", "--", "printf", "%s\\n", "hello world"))
	h.Require.Equal(`printf '%s\n' 'hello world'`, receivedCommand)
}

func Test_Sandbox_Image_ListAndPush(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	sandboxTestTokenRoute(m)
	pushStatus := "UPLOADING"
	m.SetRouteFunc("POST", "/v1/sandboxes/images", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name": "img", "status": "UPLOADING",
		})
	})
	m.SetRouteFunc("GET", "/v1/sandboxes/images", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items":      []any{map[string]any{"name": "img", "status": pushStatus, "tag_count": 2}},
			"pagination": map[string]any{"has_more": false},
		})
	})

	h.Require.NoError(h.Execute("sandbox", "image", "list"))
	h.Require.Contains(h.Stdout.String(), "img")
	h.Require.Contains(h.Stdout.String(), "UPLOADING")

	// A registry import needs no upload; without --wait the push returns at
	// the 202.
	h.Require.NoError(h.Execute("sandbox", "image", "push", "--name", "img",
		"--image", "registry.example/img:v1"))
	h.Require.Contains(h.Stdout.String(), "Name:        img")
	calls := sandboxCallsFor(m, "POST", "/v1/sandboxes/images")
	h.Require.Len(calls, 1)
	h.Require.Equal("registry.example/img:v1", calls[0].BodyJSON(t)["image"])
}

func Test_Sandbox_Image_Push_NeedsExactlyOneSource(t *testing.T) {
	h := NewCommandHarness(t)

	// The oneof group rejects both no source and two sources at parse time.
	err := h.Execute("sandbox", "image", "push", "--name", "img")
	h.Require.ErrorContains(err, "[dir image] is required")

	err = h.Execute("sandbox", "image", "push", "--name", "img", "--dir", ".", "--image", "reg/img")
	h.Require.ErrorContains(err, "none of the others can be")
}

func Test_Sandbox_Connect_RequiresTerminal(t *testing.T) {
	h := NewCommandHarness(t)

	// The harness stdin is a buffer, not a terminal, so connect refuses.
	err := h.Execute("sandbox", "connect", "--name", "sbx-1")
	h.Require.ErrorContains(err, "interactive terminal")
}

func Test_Sandbox_Connect_RequiresDeployed(t *testing.T) {
	// The terminal guard runs before the status guard and harness stdin is
	// never a terminal, so the status guard is the e2e suite's to cover.
	t.Skip("covered by the terminal-guard order and the e2e suite")
}

func Test_Sandbox_Exec_SingleArgumentPassesVerbatim(t *testing.T) {
	var receivedCommand string
	execServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		_ = json.NewDecoder(r.Body).Decode(&request)
		receivedCommand = request["command"].(string)
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = io.WriteString(w, `{"type":"result","data":"{\"command\":\"x\",\"name\":\"x\",\"pid\":\"9\",\"status\":\"completed\",\"exitCode\":0,\"stdout\":\"\",\"stderr\":\"\",\"logs\":\"\",\"workingDir\":\"/\",\"startedAt\":\"2026-09-30T22:00:01Z\",\"completedAt\":\"2026-09-30T22:00:02Z\"}"}
`)
	}))
	t.Cleanup(execServer.Close)
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	sandboxTestTokenRoute(m)
	m.SetRoute("GET", "/v1/sandboxes/instances/sbx-1", 200, sandboxRecord("sbx-1", "DEPLOYED", execServer.URL))

	// One quoted argument is the user's whole command string, quotes intact
	// for the sandbox's shell.
	h.Require.NoError(h.Execute("sandbox", "exec", "--name", "sbx-1", "--", `echo "Welcom to $PWD"`))
	h.Require.Equal(`echo "Welcom to $PWD"`, receivedCommand)
}

func Test_Sandbox_ImageLibrary_ListRenders(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	sandboxTestTokenRoute(m)
	m.SetRouteFunc("GET", "/v1/sandboxes/library_images", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"items": [
			{"name": "py-app", "display_name": "Python App", "image": "baseten/py-app:latest", "memory": 4096, "categories": ["python"]}
		]}`)
	})

	h.Require.NoError(h.Execute("sandbox", "image-library", "list"))
	h.Require.Contains(h.Stdout.String(), "Python App")
	h.Require.Contains(h.Stdout.String(), "baseten/py-app:latest")

	h.Require.NoError(h.Execute("sandbox", "image-library", "list", "--output", "json"))
	var listed cmd.SandboxLibraryImageList
	h.Require.NoError(json.Unmarshal([]byte(h.Stdout.String()), &listed))
	h.Require.Len(listed.Items, 1)
	h.Require.Equal("baseten/py-app:latest", listed.Items[0].Image)
	h.Require.NotNil(listed.Items[0].MemoryMB)
	h.Require.Equal(4096, *listed.Items[0].MemoryMB)
}

// processRecord is one process in the exec plane's list and logs fixtures.
func processRecord(pid, name, status string) map[string]any {
	return map[string]any{
		"command": "sleep 60", "name": name, "pid": pid, "status": status,
		"exitCode": 0, "stdout": "", "stderr": "", "logs": "",
		"workingDir": "/", "startedAt": "2026-09-30T22:00:01Z", "completedAt": "",
	}
}

func Test_Sandbox_Process_StartPrintsPid(t *testing.T) {
	execServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		_ = json.NewDecoder(r.Body).Decode(&request)
		// waitForCompletion is omitempty: absent means the process runs in the
		// background.
		if waiting, present := request["waitForCompletion"]; present && waiting != false {
			t.Errorf("start must not wait, got %v", waiting)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(processRecord("42", "sleep", "running"))
	}))
	t.Cleanup(execServer.Close)
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	sandboxTestTokenRoute(m)
	m.SetRoute("GET", "/v1/sandboxes/instances/sbx-1", 200, sandboxRecord("sbx-1", "DEPLOYED", execServer.URL))

	h.Require.NoError(h.Execute("sandbox", "process", "start", "--name", "sbx-1", "--", "sleep", "60"))
	h.Require.Contains(h.Stdout.String(), "Started process 42")
	h.Require.Contains(h.Stdout.String(), "process logs --name sbx-1 --pid 42")
}

func Test_Sandbox_Process_ListTable(t *testing.T) {
	execServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/process" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]any{processRecord("42", "sleep", "running")})
	}))
	t.Cleanup(execServer.Close)
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	sandboxTestTokenRoute(m)
	m.SetRoute("GET", "/v1/sandboxes/instances/sbx-1", 200, sandboxRecord("sbx-1", "DEPLOYED", execServer.URL))

	h.Require.NoError(h.Execute("sandbox", "process", "list", "--name", "sbx-1"))
	h.Require.Contains(h.Stdout.String(), "PID")
	h.Require.Contains(h.Stdout.String(), "42")
	h.Require.Contains(h.Stdout.String(), "running")
}

func Test_Sandbox_Process_LogsPrintsOutput(t *testing.T) {
	execServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/process/42/logs" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"logs": "line one\nline two\n", "stdout": "line one\nline two\n", "stderr": ""}`)
	}))
	t.Cleanup(execServer.Close)
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	sandboxTestTokenRoute(m)
	m.SetRoute("GET", "/v1/sandboxes/instances/sbx-1", 200, sandboxRecord("sbx-1", "DEPLOYED", execServer.URL))

	h.Require.NoError(h.Execute("sandbox", "process", "logs", "--name", "sbx-1", "--pid", "42"))
	h.Require.Equal("line one\nline two\n", h.Stdout.String())
}

func Test_Sandbox_Process_ExecAliasRunsSameCommand(t *testing.T) {
	var receivedCommand string
	execServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		_ = json.NewDecoder(r.Body).Decode(&request)
		receivedCommand = request["command"].(string)
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = io.WriteString(w, `{"type":"result","data":"{\"command\":\"x\",\"name\":\"x\",\"pid\":\"9\",\"status\":\"completed\",\"exitCode\":0,\"stdout\":\"\",\"stderr\":\"\",\"logs\":\"\",\"workingDir\":\"/\",\"startedAt\":\"2026-09-30T22:00:01Z\",\"completedAt\":\"2026-09-30T22:00:02Z\"}"}
`)
	}))
	t.Cleanup(execServer.Close)
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	sandboxTestTokenRoute(m)
	m.SetRoute("GET", "/v1/sandboxes/instances/sbx-1", 200, sandboxRecord("sbx-1", "DEPLOYED", execServer.URL))

	h.Require.NoError(h.Execute("sandbox", "process", "exec", "--name", "sbx-1", "--", "true"))
	h.Require.Equal("true", receivedCommand)
}
