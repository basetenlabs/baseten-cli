package harnesspoc

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ErrorMarker appears in every error this gateway returns, so a harness that
// surfaces our message can be told apart from one inventing its own.
const ErrorMarker = "harness-poc"

// Call is one request the gateway received.
type Call struct {
	Time      time.Time `json:"time"`
	Method    string    `json:"method"`
	Path      string    `json:"path"`
	Model     string    `json:"model,omitempty"`
	Stream    bool      `json:"stream,omitempty"`
	Auth      string    `json:"auth,omitempty"`
	UserAgent string    `json:"user_agent,omitempty"`
	Status    int       `json:"status"`
}

// Server is the mock gateway the configured harnesses call. It answers model
// discovery for real and refuses inference with a marked error, which is
// enough to see which model name each harness sends, when, and whether our
// message reaches the user.
type Server struct {
	// Catalog is what model discovery returns.
	Catalog Catalog
	// WithModelDiscovery registers GET /v1/models. Left false the route is not
	// registered at all, so a populated picker can only have come from the
	// harness's local model list.
	WithModelDiscovery bool
	// LogDir, when set, receives a file per request holding the body.
	LogDir string
	// Logf writes one human-readable line per request.
	Logf func(format string, args ...any)

	mu    sync.Mutex
	calls []Call
	seq   int
}

// Calls returns every request received so far.
func (s *Server) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Call(nil), s.calls...)
}

// Handler returns the gateway's HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	if s.WithModelDiscovery {
		mux.HandleFunc("GET /v1/models", s.handleModels)
	}
	mux.HandleFunc("/", s.handleInference)
	return mux
}

// requestBody reads the body, optionally dumping it to LogDir, and pulls out
// the fields worth logging.
func (s *Server) requestBody(r *http.Request) (model string, stream bool, raw []byte) {
	raw, _ = io.ReadAll(r.Body)
	var parsed struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &parsed)
	}
	return parsed.Model, parsed.Stream, raw
}

// dump writes a request body to LogDir under a sequence number, so the order
// of a harness's calls is recoverable.
func (s *Server) dump(path string, raw []byte) {
	if s.LogDir == "" || len(raw) == 0 {
		return
	}
	s.mu.Lock()
	s.seq++
	seq := s.seq
	s.mu.Unlock()
	name := fmt.Sprintf("%03d-%s.json", seq, strings.Trim(strings.ReplaceAll(path, "/", "_"), "_"))
	if err := os.MkdirAll(s.LogDir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(s.LogDir, name), raw, 0o600)
}

// record logs a call and appends it to the history.
func (s *Server) record(c Call) {
	s.mu.Lock()
	s.calls = append(s.calls, c)
	s.mu.Unlock()
	if s.Logf == nil {
		return
	}
	model := c.Model
	if model == "" {
		model = "-"
	}
	s.Logf("%s  %-4s %-28s model=%-28s stream=%-5t status=%d auth=%s ua=%s\n",
		c.Time.Format("15:04:05.000"), c.Method, c.Path, model, c.Stream, c.Status, c.Auth, c.UserAgent)
}

// authSummary reduces the credential to something loggable: the scheme and a
// short prefix, never the whole secret.
func authSummary(r *http.Request) string {
	for _, h := range []string{"Authorization", "X-Api-Key"} {
		v := r.Header.Get(h)
		if v == "" {
			continue
		}
		if len(v) > 24 {
			v = v[:24] + "..."
		}
		return h + "=" + v
	}
	return "none"
}

// modelEntry is one row of the OpenAI-shaped model list.
type modelEntry struct {
	ID          string `json:"id"`
	Object      string `json:"object"`
	Created     int64  `json:"created"`
	OwnedBy     string `json:"owned_by"`
	DisplayName string `json:"display_name,omitempty"`
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	entries := make([]modelEntry, 0, len(s.Catalog.Models))
	for _, m := range s.Catalog.Models {
		entries = append(entries, modelEntry{
			ID:          m.Slug,
			Object:      "model",
			Created:     time.Now().Unix(),
			OwnedBy:     "baseten",
			DisplayName: m.Label(),
		})
	}
	s.record(Call{
		Time:      time.Now(),
		Method:    r.Method,
		Path:      pathWithQuery(r),
		Auth:      authSummary(r),
		UserAgent: r.UserAgent(),
		Status:    http.StatusOK,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": entries})
}

func (s *Server) handleInference(w http.ResponseWriter, r *http.Request) {
	model, stream, raw := s.requestBody(r)
	path := pathWithQuery(r)
	s.dump(r.URL.Path, raw)

	// Claude Desktop pings every configured model with a one-token,
	// non-streaming request before it will offer the model in the picker, so
	// refusing everything leaves the picker empty and tells us nothing about
	// the model list. A one-token reply is answerable without pretending to be
	// a model; a real conversation turn, which streams, still gets the error.
	if !stream && strings.HasPrefix(r.URL.Path, "/v1/messages") {
		s.handleAnthropicProbe(w, r, model, path)
		return
	}

	// 400 rather than 5xx, so a harness's retry-on-server-error path does not
	// mask whether it surfaces the message.
	status := http.StatusBadRequest
	s.record(Call{
		Time:      time.Now(),
		Method:    r.Method,
		Path:      path,
		Model:     model,
		Stream:    stream,
		Auth:      authSummary(r),
		UserAgent: r.UserAgent(),
		Status:    status,
	})

	known := "unknown to the catalog"
	if _, ok := s.Catalog.Resolve(model); ok {
		known = "in the catalog"
	}
	message := fmt.Sprintf("%s: inference is not implemented. Model %q is %s.", ErrorMarker, model, known)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The error envelope differs by dialect, and a harness that cannot parse
	// the reply will report a transport error instead of our message, which
	// would hide exactly what we are testing.
	if strings.HasPrefix(r.URL.Path, "/v1/messages") {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type":  "error",
			"error": map[string]any{"type": "invalid_request_error", "message": message},
		})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"type": "invalid_request_error", "message": message, "code": "not_implemented"},
	})
}

// handleAnthropicProbe answers a non-streaming Messages request with a
// minimal, valid Anthropic response, which is what a harness's model
// availability check needs to mark the model servable.
func (s *Server) handleAnthropicProbe(w http.ResponseWriter, r *http.Request, model, path string) {
	s.record(Call{
		Time:      time.Now(),
		Method:    r.Method,
		Path:      path + "  [probe]",
		Model:     model,
		Auth:      authSummary(r),
		UserAgent: r.UserAgent(),
		Status:    http.StatusOK,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":            "msg_harness_poc",
		"type":          "message",
		"role":          "assistant",
		"model":         model,
		"content":       []map[string]any{{"type": "text", "text": "."}},
		"stop_reason":   "end_turn",
		"stop_sequence": nil,
		"usage":         map[string]any{"input_tokens": 1, "output_tokens": 1},
	})
}

// pathWithQuery renders the request target for the log, since harnesses put
// meaningful things in the query string.
func pathWithQuery(r *http.Request) string {
	if r.URL.RawQuery == "" {
		return r.URL.Path
	}
	return r.URL.Path + "?" + r.URL.RawQuery
}
