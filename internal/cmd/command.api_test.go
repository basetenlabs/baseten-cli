package cmd_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/basetenlabs/baseten-cli/internal/cmd"
)

// apiRequest captures what the server received.
type apiRequest struct {
	Method  string
	Path    string
	Headers http.Header
	Body    string
}

// TODO: Refactor into a general-purpose test helper if other command tests need
// to mock HTTP calls.
//
// newAPIHarness creates a CommandHarness with an httptest server wired in via
// context. The server records the request and responds with the given status
// and body (JSON-encoded unless body is a string).
func newAPIHarness(t *testing.T, status int, respBody any) (*CommandHarness, *apiRequest) {
	captured := &apiRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.Method = r.Method
		captured.Path = r.URL.Path
		captured.Headers = r.Header.Clone()
		b, _ := io.ReadAll(r.Body)
		captured.Body = string(b)

		switch body := respBody.(type) {
		case string:
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(status)
			w.Write([]byte(body))
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(body)
		}
	}))
	t.Cleanup(srv.Close)

	h := NewCommandHarness(t)
	h.T.Setenv("BASETEN_BASE_URL", srv.URL)
	h.T.Setenv("BASETEN_INFERENCE_URL", srv.URL)
	h.Context = cmd.WithHTTPClient(h.Context, srv.Client())
	return h, captured
}

func Test_API_Management_DefaultGET(t *testing.T) {
	h, req := newAPIHarness(t, 200, map[string]any{"ok": true})
	err := h.Execute("api", "management", "models")
	h.Require.NoError(err)
	h.Require.Equal("GET", req.Method)
	h.Require.Equal("/v1/models", req.Path)
	h.Require.Equal("{\n  \"ok\": true\n}\n", h.Stdout.String())
}

func Test_API_Management_JSONLOutput(t *testing.T) {
	h, _ := newAPIHarness(t, 200, map[string]any{"ok": true})
	err := h.Execute("api", "management", "-o", "jsonl", "models")
	h.Require.NoError(err)
	h.Require.Equal("{\"ok\":true}\n", h.Stdout.String())
}

func Test_API_Management_AcceptsV1Prefix(t *testing.T) {
	for _, path := range []string{"models", "/v1/models", "v1/models"} {
		h, req := newAPIHarness(t, 200, map[string]any{"ok": true})
		err := h.Execute("api", "management", path)
		h.Require.NoError(err)
		h.Require.Equal("/v1/models", req.Path)
	}
}

func Test_API_Management_AuthHeader(t *testing.T) {
	h, req := newAPIHarness(t, 200, map[string]any{})
	err := h.Execute("api", "management", "models")
	h.Require.NoError(err)
	h.Require.Equal("Api-Key test-key", req.Headers.Get("Authorization"))
}

func Test_API_Management_ExplicitMethod(t *testing.T) {
	h, req := newAPIHarness(t, 200, "")
	err := h.Execute("api", "management", "-X", "DELETE", "models/abc")
	h.Require.NoError(err)
	h.Require.Equal("DELETE", req.Method)
	h.Require.Equal("/v1/models/abc", req.Path)
	h.Require.Equal("", h.Stdout.String())
}

func Test_API_Management_FieldDefaultsPOST(t *testing.T) {
	h, req := newAPIHarness(t, 200, map[string]any{})
	err := h.Execute("api", "management", "-F", `name="my-model"`, "models")
	h.Require.NoError(err)
	h.Require.Equal("POST", req.Method)
	h.Require.Equal(`{"name":"my-model"}`, req.Body)
}

func Test_API_Management_RawField(t *testing.T) {
	h, req := newAPIHarness(t, 200, map[string]any{})
	err := h.Execute("api", "management", "-f", "name=raw-value", "models")
	h.Require.NoError(err)
	h.Require.Equal("POST", req.Method)
	h.Require.Equal(`{"name":"raw-value"}`, req.Body)
}

func Test_API_Management_MixedFields(t *testing.T) {
	h, req := newAPIHarness(t, 200, map[string]any{})
	err := h.Execute("api", "management",
		"-f", "name=my-model",
		"-F", "count=42",
		"-F", `tags=["a","b"]`,
		"models",
	)
	h.Require.NoError(err)
	var body map[string]any
	h.Require.NoError(json.Unmarshal([]byte(req.Body), &body))
	h.Require.Equal("my-model", body["name"])
	h.Require.Equal(float64(42), body["count"])
	h.Require.Equal([]any{"a", "b"}, body["tags"])
}

func Test_API_Management_CustomHeader(t *testing.T) {
	h, req := newAPIHarness(t, 200, map[string]any{})
	err := h.Execute("api", "management", "-H", "X-Custom: my-value", "models")
	h.Require.NoError(err)
	h.Require.Equal("my-value", req.Headers.Get("X-Custom"))
}

func Test_API_Management_InputFile(t *testing.T) {
	h, req := newAPIHarness(t, 200, map[string]any{})
	tmpFile := filepath.Join(t.TempDir(), "body.json")
	h.Require.NoError(os.WriteFile(tmpFile, []byte(`{"from":"file"}`), 0644))
	err := h.Execute("api", "management", "--input", tmpFile, "models")
	h.Require.NoError(err)
	h.Require.Equal("POST", req.Method)
	h.Require.Equal(`{"from":"file"}`, req.Body)
}

func Test_API_Management_InputFieldMutuallyExclusive(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("api", "management", "--input", "file.json", "-f", "key=val", "models")
	h.Require.ErrorContains(err, "mutually exclusive")
}

func Test_API_Management_JQFilter(t *testing.T) {
	h, _ := newAPIHarness(t, 200, map[string]any{
		"models": []any{
			map[string]any{"name": "first"},
			map[string]any{"name": "second"},
		},
	})
	err := h.Execute("api", "management", "--jq", ".models[].name", "models")
	h.Require.NoError(err)
	h.Require.Equal("\"first\"\n\"second\"\n", h.Stdout.String())
}

func Test_API_Management_JQOnNonJSON(t *testing.T) {
	h, _ := newAPIHarness(t, 200, "plain text")
	_ = h.Execute("api", "management", "--jq", ".foo", "models")
	h.Require.True(h.Exited())
	h.Require.Contains(h.Stderr.String(), "--jq can only be used with JSON")
}

func Test_API_Management_NonJSONResponse(t *testing.T) {
	h, _ := newAPIHarness(t, 200, "hello plain")
	err := h.Execute("api", "management", "something")
	h.Require.NoError(err)
	h.Require.Equal("hello plain", h.Stdout.String())
}

func Test_API_Management_HTTPError(t *testing.T) {
	h, _ := newAPIHarness(t, 404, map[string]any{"error": "not found"})
	_ = h.Execute("api", "management", "models/bad")
	h.Require.True(h.Exited())
	h.Require.Contains(h.Stderr.String(), "status 404")
}

func Test_API_Management_PathNormalization(t *testing.T) {
	h, req := newAPIHarness(t, 200, map[string]any{})
	err := h.Execute("api", "management", "/models")
	h.Require.NoError(err)
	h.Require.Equal("/v1/models", req.Path)
}

func Test_API_Management_NoneOutput(t *testing.T) {
	h, _ := newAPIHarness(t, 200, map[string]any{"key": "value"})
	err := h.Execute("api", "management", "-o", "none", "models")
	h.Require.NoError(err)
	h.Require.Equal("", h.Stdout.String())
}

func Test_API_Management_RequiresPath(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("api", "management")
	h.Require.ErrorContains(err, "accepts 1 arg")
}

func Test_API_Management_RequiresAPIKey(t *testing.T) {
	h := NewCommandHarness(t)
	h.T.Setenv("BASETEN_API_KEY", "")
	h.Require.Error(h.Execute("api", "management", "some/path"))
	h.Require.Equal(1, h.ExitCode)
	h.Require.Contains(h.Stderr.String(), "BASETEN_API_KEY")
}

func Test_API_Help(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("api", "--help")
	h.Require.NoError(err)
	h.Require.Contains(h.Stdout.String(), "management")
	h.Require.Contains(h.Stdout.String(), "inference")
}

func Test_API_Management_ParentLevelFlag(t *testing.T) {
	// Verify that flags placed on the parent ("api -o jsonl") propagate to the child.
	h, _ := newAPIHarness(t, 200, map[string]any{"ok": true})
	err := h.Execute("api", "-o", "jsonl", "management", "models")
	h.Require.NoError(err)
	h.Require.Equal("{\"ok\":true}\n", h.Stdout.String())
}

// Inference-specific tests

func Test_API_Inference_RequiresPath(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("api", "inference")
	h.Require.ErrorContains(err, "accepts 1 arg")
}

func Test_API_Inference_RequiresModelOrChain(t *testing.T) {
	h := NewCommandHarness(t)
	h.T.Setenv("BASETEN_BASE_URL", "")
	err := h.Execute("api", "inference", "predict")
	h.Require.ErrorContains(err, "model ID or chain ID")
}

func Test_API_Inference_ModelAndChainMutuallyExclusive(t *testing.T) {
	h := NewCommandHarness(t)
	h.T.Setenv("BASETEN_BASE_URL", "")
	err := h.Execute("api", "inference", "--model-id", "abc", "--chain-id", "xyz", "predict")
	h.Require.ErrorContains(err, "mutually exclusive")
}

func Test_API_Inference_BaseURLOverride(t *testing.T) {
	h, req := newAPIHarness(t, 200, map[string]any{"result": "ok"})
	err := h.Execute("api", "inference", "predict")
	h.Require.NoError(err)
	h.Require.Equal("GET", req.Method)
	h.Require.Equal("/predict", req.Path)
	h.Require.Equal("{\n  \"result\": \"ok\"\n}\n", h.Stdout.String())
}

func Test_API_Inference_FieldsPostToServer(t *testing.T) {
	h, req := newAPIHarness(t, 200, map[string]any{"output": "result"})
	err := h.Execute("api", "inference", "-F", `prompt="hello"`, "predict")
	h.Require.NoError(err)
	h.Require.Equal("POST", req.Method)
	h.Require.Equal(`{"prompt":"hello"}`, req.Body)
	h.Require.Equal("{\n  \"output\": \"result\"\n}\n", h.Stdout.String())
}
