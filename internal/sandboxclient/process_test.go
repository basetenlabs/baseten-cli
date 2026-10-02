package sandboxclient_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/basetenlabs/baseten-cli/internal/sandboxclient"
)

const testProcessRecord = `{
	"command": "echo hi", "completedAt": "2026-09-30T10:00:03Z", "exitCode": 0,
	"logs": "hi\n", "name": "echo", "pid": "123",
	"startedAt": "2026-09-30T10:00:01Z", "status": "completed",
	"stdout": "hi\n", "stderr": "", "workingDir": "/tmp"
}`

// execPlaneForTest serves one sandbox's execution API and records the
// Authorization header of the last request.
func execPlaneForTest(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func sandboxForExecTest(t *testing.T, execServerURL string, token string) *sandboxclient.Sandbox {
	t.Helper()
	instance, err := sandboxclient.NewSandbox(sandboxclient.SandboxOptions{
		Name:  "sbx-1",
		URL:   execServerURL,
		Token: token,
	})
	if err != nil {
		t.Fatal(err)
	}
	return instance
}

func TestExecConvertsProcessRecord(t *testing.T) {
	server := execPlaneForTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/process" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer exec-token" {
			t.Errorf("authorization %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, testProcessRecord)
	})
	instance := sandboxForExecTest(t, server.URL, "exec-token")

	info, err := instance.Process().Exec(context.Background(), &sandboxclient.ExecOptions{
		Command:           "echo hi",
		WaitForCompletion: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if info.ExitCode != 0 || info.Stdout != "hi\n" || info.Status != "completed" {
		t.Errorf("process not converted: %+v", info)
	}
	if info.CompletedAt.IsZero() || info.StartedAt.IsZero() {
		t.Errorf("timestamps not parsed: started %v completed %v", info.StartedAt, info.CompletedAt)
	}
}

func TestExecSendsWaitForCompletion(t *testing.T) {
	var body string
	server := execPlaneForTest(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, testProcessRecord)
	})
	instance := sandboxForExecTest(t, server.URL, "exec-token")

	_, err := instance.Process().Exec(context.Background(), &sandboxclient.ExecOptions{
		Command:           "echo hi",
		WorkingDir:        "/work",
		Env:               map[string]string{"A": "1"},
		TimeoutSeconds:    30,
		WaitForCompletion: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"command":"echo hi"`, `"waitForCompletion":true`, `"workingDir":"/work"`,
		`"env":{"A":"1"}`, `"timeout":30`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body %q missing %s", body, want)
		}
	}
}

func TestProcessListConvertsRecords(t *testing.T) {
	server := execPlaneForTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/process" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "["+testProcessRecord+"]")
	})
	instance := sandboxForExecTest(t, server.URL, "exec-token")

	infos, err := instance.Process().List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].PID != "123" || infos[0].Status != "completed" {
		t.Errorf("processes not converted: %+v", infos)
	}
}

func TestProcessLogsReturnsCapturedOutput(t *testing.T) {
	server := execPlaneForTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/process/123/logs" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"logs": "hi\n", "stdout": "hi\n", "stderr": ""}`)
	})
	instance := sandboxForExecTest(t, server.URL, "exec-token")

	logs, err := instance.Process().Logs(context.Background(), "123")
	if err != nil {
		t.Fatal(err)
	}
	if logs.Stdout != "hi\n" || logs.Logs != "hi\n" {
		t.Errorf("logs not converted: %+v", logs)
	}
}

func TestExecGatewayErrorTyped(t *testing.T) {
	server := execPlaneForTest(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(502)
	})
	instance := sandboxForExecTest(t, server.URL, "exec-token")

	_, err := instance.Process().Exec(context.Background(), &sandboxclient.ExecOptions{Command: "true"})
	var gatewayError *sandboxclient.SandboxGatewayError
	if !errors.As(err, &gatewayError) || gatewayError.Status != 502 {
		t.Fatalf("want a SandboxGatewayError, got %v", err)
	}
}

func TestExecErrorBodyBecomesSandboxError(t *testing.T) {
	server := execPlaneForTest(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		_, _ = io.WriteString(w, `{"error": "command is empty"}`)
	})
	instance := sandboxForExecTest(t, server.URL, "exec-token")

	_, err := instance.Process().Exec(context.Background(), &sandboxclient.ExecOptions{Command: ""})
	var apiError *sandboxclient.SandboxAPIError
	if !errors.As(err, &apiError) {
		t.Fatalf("want SandboxAPIError, got %v", err)
	}
	if apiError.Status != 400 {
		t.Errorf("status %d", apiError.Status)
	}
	if !strings.Contains(apiError.Error(), "command is empty") {
		t.Errorf("message %q", apiError.Error())
	}
}

func TestExecStreamYieldsEventsUntilResult(t *testing.T) {
	server := execPlaneForTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("streaming requires the event-stream accept, got %q", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		// The wire format the server sends despite the event-stream accept.
		// The result event's data carries the final record as JSON.
		_, _ = io.WriteString(w, `{"type":"stdout","data":"counting"}
`)
		_, _ = io.WriteString(w, `{"type":"keepalive","data":""}
`)
		_, _ = io.WriteString(w, `{"type":"stderr","data":"to stderr"}
`)
		_, _ = io.WriteString(w, `{"type":"result","data":"{\"command\":\"count\",\"name\":\"count\",\"pid\":\"9\",\"status\":\"completed\",\"exitCode\":0,\"stdout\":\"counting\\n\",\"stderr\":\"to stderr\\n\",\"logs\":\"counting\\n\",\"workingDir\":\"/\",\"startedAt\":\"Tue, 30 Sep 2026 10:00:01 GMT\",\"completedAt\":\"Tue, 30 Sep 2026 10:00:03 GMT\"}"}
`)
	})
	instance := sandboxForExecTest(t, server.URL, "exec-token")

	var events []sandboxclient.ExecEvent
	for event, err := range instance.Process().ExecStream(context.Background(), &sandboxclient.ExecOptions{Command: "count"}) {
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	if len(events) != 3 {
		t.Fatalf("keepalive must not yield; got %d events", len(events))
	}
	if events[0].Type != sandboxclient.ExecEventStdout || events[0].Text != "counting" {
		t.Errorf("first event %+v", events[0])
	}
	if events[1].Type != sandboxclient.ExecEventStderr || events[1].Text != "to stderr" {
		t.Errorf("second event %+v", events[1])
	}
	finalResult := events[2]
	if finalResult.Type != sandboxclient.ExecEventResult || finalResult.Result == nil {
		t.Fatalf("last event %+v", finalResult)
	}
	if finalResult.Result.ExitCode != 0 || finalResult.Result.Status != "completed" {
		t.Errorf("result record %+v", finalResult.Result)
	}
	if finalResult.Result.CompletedAt.IsZero() {
		t.Errorf("RFC1123 timestamp not parsed: %v", finalResult.Result.CompletedAt)
	}
}

func TestExecStreamServerErrorEventFails(t *testing.T) {
	server := execPlaneForTest(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = io.WriteString(w, `{"type":"error","data":"command is empty"}
`)
	})
	instance := sandboxForExecTest(t, server.URL, "exec-token")

	var streamErr error
	for _, err := range instance.Process().ExecStream(context.Background(), &sandboxclient.ExecOptions{Command: ""}) {
		if err != nil {
			streamErr = err
		}
	}
	if streamErr == nil || !strings.Contains(streamErr.Error(), "command is empty") {
		t.Fatalf("want the server error event as an error, got %v", streamErr)
	}
}

func TestExecStreamEarlyStopClosesIteration(t *testing.T) {
	server := execPlaneForTest(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = io.WriteString(w, `{"type":"stdout","data":"y"}
`)
	})
	instance := sandboxForExecTest(t, server.URL, "exec-token")

	count := 0
	for _, err := range instance.Process().ExecStream(context.Background(), &sandboxclient.ExecOptions{Command: "yes"}) {
		if err != nil {
			t.Fatal(err)
		}
		count++
		break
	}
	if count != 1 {
		t.Errorf("expected exactly one event before the break, got %d", count)
	}
}

func TestStaticTokenSandboxFromClientSharesRevocation(t *testing.T) {
	// A token revoked on the sandbox must be dropped from the client's
	// cache, so the client's next control-plane call re-mints.
	recorder := &controlPlaneRecorder{}
	controlServer := recorder.serve(t, testSandboxRecord)
	client := clientForTest(t, controlServer.URL, nil)

	if _, err := client.GetInfo(context.Background(), "sbx-1", nil); err != nil {
		t.Fatal(err)
	}
	execServer := execPlaneForTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer tok-1" {
			w.Header().Set("x-blaxel-error-code", "TOKEN_REVOKED")
			w.WriteHeader(401)
			_, _ = io.WriteString(w, `{"error": "TOKEN_REVOKED"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, testProcessRecord)
	})
	execRecord := sandboxclient.SandboxInfo{Name: "sbx-1", URL: execServer.URL, Status: "DEPLOYED"}
	instance, err := client.SandboxFromInfo(execRecord)
	if err != nil {
		t.Fatal(err)
	}

	_, err = instance.Process().Exec(context.Background(), &sandboxclient.ExecOptions{Command: "echo hi"})
	if err != nil {
		t.Fatal(err)
	}
	if len(recorder.mints) != 2 {
		t.Errorf("the revoked token must be dropped and re-minted, got %d mints", len(recorder.mints))
	}
	if recorder.countByPath("/v1/sandboxes/instances/sbx-1") < 1 {
		t.Errorf("expected the exec plane revocation to share the client's cache")
	}
}

func TestExecTimestampUnparseableErrors(t *testing.T) {
	server := execPlaneForTest(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, strings.Replace(testProcessRecord, "2026-09-30T10:00:01Z", "not a time", 1))
	})
	instance := sandboxForExecTest(t, server.URL, "exec-token")

	_, err := instance.Process().Exec(context.Background(), &sandboxclient.ExecOptions{Command: "echo hi"})
	if err == nil || !strings.Contains(err.Error(), "timestamp") {
		t.Fatalf("want a timestamp error, got %v", err)
	}
}

func TestNewSandboxWithoutURLErrors(t *testing.T) {
	_, err := sandboxclient.NewSandbox(sandboxclient.SandboxOptions{Name: "sbx-1", Token: "t"})
	if err == nil || !strings.Contains(err.Error(), "no URL") {
		t.Fatalf("want a no-URL error, got %v", err)
	}
}

func TestNewSandboxTokenWithProviderRejected(t *testing.T) {
	_, err := sandboxclient.NewSandbox(sandboxclient.SandboxOptions{
		URL:           "https://sbx.invalid",
		Token:         "t",
		TokenProvider: func(context.Context) (string, error) { return "t", nil },
	})
	if err == nil {
		t.Fatal("Token with TokenProvider must error")
	}
}

func TestExecOptionsZeroValuesOmitted(t *testing.T) {
	var body string
	server := execPlaneForTest(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, testProcessRecord)
	})
	instance := sandboxForExecTest(t, server.URL, "exec-token")

	if _, err := instance.Process().Exec(context.Background(), &sandboxclient.ExecOptions{Command: "true"}); err != nil {
		t.Fatal(err)
	}
	for _, mustBeAbsent := range []string{"workingDir", `"env"`, `"timeout"`, `"name"`} {
		if strings.Contains(body, mustBeAbsent) {
			t.Errorf("zero-value option %s sent: %s", mustBeAbsent, body)
		}
	}
	if !strings.Contains(body, `"waitForCompletion":false`) {
		t.Errorf("waitForCompletion must be explicit so exec does not hang: %s", body)
	}
}

func TestInfoTimestampsParseFromRecord(t *testing.T) {
	created := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	recorder := &controlPlaneRecorder{}
	server := recorder.serve(t, testSandboxRecord)
	client := clientForTest(t, server.URL, nil)

	info, err := client.GetInfo(context.Background(), "sbx-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !info.CreatedAt.Equal(created) {
		t.Errorf("created_at not parsed: %v", info.CreatedAt)
	}
	if info.Envs["B"] != (sandboxclient.SandboxEnvValue{Value: "2", Secret: true}) {
		t.Errorf("secret env not converted: %+v", info.Envs)
	}
}

func TestExecStreamEndedWithoutResultErrors(t *testing.T) {
	server := execPlaneForTest(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = io.WriteString(w, `{"type":"stdout","data":"y"}
`)
	})
	instance := sandboxForExecTest(t, server.URL, "exec-token")

	var streamErr error
	for _, err := range instance.Process().ExecStream(context.Background(), &sandboxclient.ExecOptions{Command: "yes"}) {
		if err != nil {
			streamErr = err
		}
	}
	if streamErr == nil || !strings.Contains(streamErr.Error(), "without a result") {
		t.Fatalf("want a stream-truncation error, got %v", streamErr)
	}
}
