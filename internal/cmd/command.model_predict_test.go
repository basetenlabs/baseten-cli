package cmd_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coder/websocket"
)

// newPredictHarness wires a CommandHarness to a MockManagementAPI and points
// the inference base URL at it. Returns the harness so tests can register
// per-route predict handlers.
func newPredictHarness(t *testing.T) (*CommandHarness, *MockManagementAPI) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	t.Setenv("BASETEN_INFERENCE_BASE_URL_OVERRIDE", m.URL)
	return h, m
}

func Test_Model_Predict_DefaultEnvironment(t *testing.T) {
	h, m := newPredictHarness(t)
	m.SetRoute("POST", "/environments/production/predict", 200, map[string]any{"result": "ok"})

	err := h.Execute("model", "predict", "--model-id", "m-1", "--data", `{"x":1}`)
	h.Require.NoError(err)
	call := m.FindCall("POST", "/environments/production/predict")
	h.Require.NotNil(call)
	h.Require.Equal(`{"x":1}`, call.Body)
	h.Require.Contains(h.Stdout.String(), `"result"`)
}

func Test_Model_Predict_NamedEnvironment(t *testing.T) {
	h, m := newPredictHarness(t)
	m.SetRoute("POST", "/environments/development/predict", 200, map[string]any{"r": 1})

	err := h.Execute("model", "predict", "--model-id", "m-1", "--environment", "development", "--data", `{}`)
	h.Require.NoError(err)
	h.Require.NotNil(m.FindCall("POST", "/environments/development/predict"))
}

func Test_Model_Predict_DeploymentID(t *testing.T) {
	h, m := newPredictHarness(t)
	m.SetRoute("POST", "/deployment/d-1/predict", 200, map[string]any{"r": 1})

	err := h.Execute("model", "predict", "--model-id", "m-1", "--deployment-id", "d-1", "--data", `{}`)
	h.Require.NoError(err)
	h.Require.NotNil(m.FindCall("POST", "/deployment/d-1/predict"))
}

func Test_Model_Predict_Regional(t *testing.T) {
	h, m := newPredictHarness(t)
	m.SetRoute("POST", "/predict", 200, map[string]any{"r": 1})

	err := h.Execute("model", "predict", "--model-id", "m-1", "--regional", "prod-us", "--data", `{}`)
	h.Require.NoError(err)
	h.Require.NotNil(m.FindCall("POST", "/predict"))
}

func Test_Model_Predict_TargetsMutuallyExclusive(t *testing.T) {
	h, _ := newPredictHarness(t)
	err := h.Execute("model", "predict", "--model-id", "m-1",
		"--environment", "production", "--deployment-id", "d-1", "--data", `{}`)
	h.Require.ErrorContains(err, "mutually exclusive")
}

func Test_Model_Predict_InputOneofRequired(t *testing.T) {
	h, _ := newPredictHarness(t)
	err := h.Execute("model", "predict", "--model-id", "m-1")
	h.Require.Error(err)
}

func Test_Model_Predict_InputOneofExclusive(t *testing.T) {
	h, _ := newPredictHarness(t)
	err := h.Execute("model", "predict", "--model-id", "m-1", "--data", `{}`, "--file", "x.json")
	h.Require.Error(err)
}

func Test_Model_Predict_FromFile(t *testing.T) {
	h, m := newPredictHarness(t)
	m.SetRoute("POST", "/environments/production/predict", 200, map[string]any{"ok": true})

	path := filepath.Join(t.TempDir(), "in.json")
	h.Require.NoError(os.WriteFile(path, []byte(`{"from":"file"}`), 0o600))

	err := h.Execute("model", "predict", "--model-id", "m-1", "--file", path)
	h.Require.NoError(err)
	call := m.FindCall("POST", "/environments/production/predict")
	h.Require.Equal(`{"from":"file"}`, call.Body)
}

func Test_Model_Predict_FromStdin(t *testing.T) {
	h, m := newPredictHarness(t)
	m.SetRoute("POST", "/environments/production/predict", 200, map[string]any{"ok": true})
	h.Stdin.WriteString(`{"from":"stdin"}`)

	err := h.Execute("model", "predict", "--model-id", "m-1", "--file", "-")
	h.Require.NoError(err)
	call := m.FindCall("POST", "/environments/production/predict")
	h.Require.Equal(`{"from":"stdin"}`, call.Body)
}

func Test_Model_Predict_OutputJSONPretty(t *testing.T) {
	h, m := newPredictHarness(t)
	m.SetRoute("POST", "/environments/production/predict", 200, map[string]any{"result": 42})

	err := h.Execute("model", "predict", "--model-id", "m-1", "--data", `{}`, "--output", "json")
	h.Require.NoError(err)
	h.Require.Equal("{\n  \"result\": 42\n}\n", h.Stdout.String())
}

func Test_Model_Predict_OutputJSONLCompact(t *testing.T) {
	h, m := newPredictHarness(t)
	m.SetRoute("POST", "/environments/production/predict", 200, map[string]any{"result": 42})

	err := h.Execute("model", "predict", "--model-id", "m-1", "--data", `{}`, "--output", "jsonl")
	h.Require.NoError(err)
	h.Require.Equal("{\"result\":42}\n", h.Stdout.String())
}

func Test_Model_Predict_BinaryResponse_Text(t *testing.T) {
	h, m := newPredictHarness(t)
	m.SetRouteFunc("POST", "/environments/production/predict", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", "5")
		_, _ = w.Write([]byte("\x00\x01\x02\x03\x04"))
	})

	err := h.Execute("model", "predict", "--model-id", "m-1", "--data", `{}`)
	h.Require.NoError(err)
	h.Require.Equal("\x00\x01\x02\x03\x04", h.Stdout.String())
}

func Test_Model_Predict_BinaryResponse_JSONEnvelope(t *testing.T) {
	h, m := newPredictHarness(t)
	payload := []byte("\x00\x01\x02\x03\x04")
	m.SetRouteFunc("POST", "/environments/production/predict", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		_, _ = w.Write(payload)
	})

	err := h.Execute("model", "predict", "--model-id", "m-1", "--data", `{}`, "--output", "jsonl")
	h.Require.NoError(err)
	expected := fmt.Sprintf("{\"body\":%q}\n", base64.StdEncoding.EncodeToString(payload))
	h.Require.Equal(expected, h.Stdout.String())
}

func Test_Model_Predict_SSE_Text_RawPassthrough(t *testing.T) {
	h, m := newPredictHarness(t)
	m.SetRouteFunc("POST", "/environments/production/predict", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = fmt.Fprint(w, "data: {\"i\":1}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	})

	err := h.Execute("model", "predict", "--model-id", "m-1", "--data", `{}`)
	h.Require.NoError(err)
	h.Require.Contains(h.Stdout.String(), `data: {"i":1}`)
	h.Require.Contains(h.Stdout.String(), "data: [DONE]")
}

func Test_Model_Predict_SSE_JSONL(t *testing.T) {
	h, m := newPredictHarness(t)
	m.SetRouteFunc("POST", "/environments/production/predict", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = fmt.Fprint(w, "data: {\"i\":1}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "data: {\"i\":2}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	})

	err := h.Execute("model", "predict", "--model-id", "m-1", "--data", `{}`, "--output", "jsonl")
	h.Require.NoError(err)
	lines := strings.Split(strings.TrimRight(h.Stdout.String(), "\n"), "\n")
	h.Require.Equal([]string{`{"i":1}`, `{"i":2}`}, lines)
	h.Require.NotContains(h.Stderr.String(), "warning")
}

func Test_Model_Predict_SSE_JSON_WarnsAndJSONL(t *testing.T) {
	h, m := newPredictHarness(t)
	m.SetRouteFunc("POST", "/environments/production/predict", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = fmt.Fprint(w, "data: {\"i\":1}\n\n")
		flusher.Flush()
	})

	err := h.Execute("model", "predict", "--model-id", "m-1", "--data", `{}`, "--output", "json")
	h.Require.NoError(err)
	h.Require.Equal("{\"i\":1}\n", h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "output will be JSONL")
}

func Test_Model_Predict_SSE_JSONL_NonJSONDataWrapped(t *testing.T) {
	h, m := newPredictHarness(t)
	m.SetRouteFunc("POST", "/environments/production/predict", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = fmt.Fprint(w, "data: hello\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	})

	err := h.Execute("model", "predict", "--model-id", "m-1", "--data", `{}`, "--output", "jsonl")
	h.Require.NoError(err)
	expected := fmt.Sprintf("{\"body\":%q}\n", base64.StdEncoding.EncodeToString([]byte("hello")))
	h.Require.Equal(expected, h.Stdout.String())
}

func Test_Model_Predict_NonSSEStream_JSONLWrapsChunks(t *testing.T) {
	h, m := newPredictHarness(t)
	chunks := [][]byte{[]byte("alpha"), []byte("beta")}
	m.SetRouteFunc("POST", "/environments/production/predict", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		flusher := w.(http.Flusher)
		for _, c := range chunks {
			_, _ = w.Write(c)
			flusher.Flush()
		}
	})

	err := h.Execute("model", "predict", "--model-id", "m-1", "--data", `{}`, "--output", "jsonl")
	h.Require.NoError(err)

	// Output is one envelope per Read; we don't assert exact chunking, only
	// that every line is a valid `{"body": "<b64>"}` and the decoded
	// concatenation matches the full payload.
	var got []byte
	for _, line := range strings.Split(strings.TrimRight(h.Stdout.String(), "\n"), "\n") {
		var env struct {
			Body string `json:"body"`
		}
		h.Require.NoError(json.Unmarshal([]byte(line), &env))
		decoded, err := base64.StdEncoding.DecodeString(env.Body)
		h.Require.NoError(err)
		got = append(got, decoded...)
	}
	h.Require.Equal([]byte("alphabeta"), got)
}

func Test_Model_Predict_ErrorStatus(t *testing.T) {
	h, m := newPredictHarness(t)
	m.SetRoute("POST", "/environments/production/predict", 500, map[string]any{"error": "boom"})

	err := h.Execute("model", "predict", "--model-id", "m-1", "--data", `{}`)
	h.Require.ErrorContains(err, "500")
}

func Test_Model_Predict_Websocket_Echo(t *testing.T) {
	h, m := newPredictHarness(t)
	var serverGot string
	m.SetRouteFunc("GET", "/environments/production/websocket", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer conn.Close(websocket.StatusInternalError, "")
		_, msg, err := conn.Read(r.Context())
		if err != nil {
			t.Errorf("read: %v", err)
			return
		}
		serverGot = string(msg)
		reply := []byte(`{"echo":` + string(msg) + `}`)
		if err := conn.Write(r.Context(), websocket.MessageText, reply); err != nil {
			t.Errorf("write: %v", err)
			return
		}
		_ = conn.Close(websocket.StatusNormalClosure, "")
	})

	err := h.Execute("model", "predict", "--model-id", "m-1", "--websocket", "--data", `{"x":1}`)
	h.Require.NoError(err)
	h.Require.Equal(`{"x":1}`, serverGot)
	h.Require.Contains(h.Stdout.String(), `"echo"`)
}

func Test_Model_Predict_Websocket_BinaryReply(t *testing.T) {
	h, m := newPredictHarness(t)
	payload := []byte{0x00, 0x01, 0x02, 0x03}
	m.SetRouteFunc("GET", "/environments/production/websocket", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer conn.Close(websocket.StatusInternalError, "")
		_, _, err = conn.Read(r.Context())
		if err != nil {
			t.Errorf("read: %v", err)
			return
		}
		if err := conn.Write(r.Context(), websocket.MessageBinary, payload); err != nil {
			t.Errorf("write: %v", err)
			return
		}
		_ = conn.Close(websocket.StatusNormalClosure, "")
	})

	err := h.Execute("model", "predict", "--model-id", "m-1", "--websocket", "--data", `{}`, "--output", "jsonl")
	h.Require.NoError(err)
	expected := fmt.Sprintf("{\"body\":%q}\n", base64.StdEncoding.EncodeToString(payload))
	h.Require.Equal(expected, h.Stdout.String())
}

func Test_Model_Predict_Websocket_DialFailure(t *testing.T) {
	h, m := newPredictHarness(t)
	// No route registered → mock returns 404, which fails the WS upgrade.
	_ = m

	err := h.Execute("model", "predict", "--model-id", "m-1", "--websocket", "--data", `{}`)
	h.Require.ErrorContains(err, "websocket dial")
}
