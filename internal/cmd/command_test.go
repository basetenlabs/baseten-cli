package cmd_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/basetenlabs/baseten-cli/internal/cmd"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

func init() {
	// Use an in-memory keyring for the entire test package so auth commands
	// never touch the developer's real system keychain.
	keyring.MockInit()
}

// CommandHarness runs CLI commands and captures output for testing.
type CommandHarness struct {
	T       *testing.T
	Require *require.Assertions
	Context context.Context
	Stdin   bytes.Buffer
	Stdout  bytes.Buffer
	Stderr  bytes.Buffer

	ExitCode int
	exited   bool

	mockManagementAPI *MockManagementAPI
}

// NewCommandHarness sets sensible env defaults and returns a fresh harness.
// Tests can override any of these with a subsequent t.Setenv before Execute.
func NewCommandHarness(t *testing.T) *CommandHarness {
	t.Setenv("BASETEN_API_KEY", "test-key")
	t.Setenv("BASETEN_REMOTE_URL", "http://127.0.0.1:1")
	t.Setenv("BASETEN_MANAGEMENT_API_URL_OVERRIDE", "http://127.0.0.1:1")
	t.Setenv("BASETEN_CONFIG_DIR", t.TempDir())
	return &CommandHarness{T: t, Require: require.New(t), Context: t.Context()}
}

func (h *CommandHarness) Execute(args ...string) error {
	h.Stdout.Reset()
	h.Stderr.Reset()
	h.ExitCode = 0
	h.exited = false
	cmd.VerifyRunners()
	err := cmd.Execute(h.Context, cmd.ExecuteOptions{
		Args:   args,
		Stdin:  &h.Stdin,
		Stdout: &h.Stdout,
		Stderr: &h.Stderr,
		ExitWithCode: func(code int) {
			h.ExitCode = code
			h.exited = true
		},
	})
	if err != nil && !h.exited {
		h.ExitCode = 1
	}
	if err == nil && h.exited && h.ExitCode != 0 {
		return fmt.Errorf("command exited with code %d: %s", h.ExitCode, h.Stderr.String())
	}
	return err
}

func (h *CommandHarness) Exited() bool {
	return h.exited
}

// MockManagementAPI returns a lazily created fake management API server
// wired into the harness. The server is closed via t.Cleanup. Successive
// calls return the same instance.
func (h *CommandHarness) MockManagementAPI() *MockManagementAPI {
	if h.mockManagementAPI != nil {
		return h.mockManagementAPI
	}
	m := &MockManagementAPI{routes: map[string]http.HandlerFunc{}}
	m.server = httptest.NewServer(http.HandlerFunc(m.serve))
	m.URL = m.server.URL
	h.T.Cleanup(m.server.Close)
	h.T.Setenv("BASETEN_MANAGEMENT_API_URL_OVERRIDE", m.URL)
	h.Context = cmd.WithHTTPClient(h.Context, m.server.Client())
	h.mockManagementAPI = m
	return m
}

// MockAPICall captures a single request received by MockManagementAPI.
type MockAPICall struct {
	Method string
	Path   string
	Body   string
}

// MockManagementAPI is a fake management API backed by httptest.Server.
// Register specific routes via SetRoute or SetRouteFunc; use SetHandler to
// supply a fallthrough for any request that no route matches. Without a
// fallthrough, unrouted requests return 404.
type MockManagementAPI struct {
	URL    string
	server *httptest.Server

	mu       sync.Mutex
	calls    []MockAPICall
	routes   map[string]http.HandlerFunc
	fallback http.HandlerFunc
}

func (m *MockManagementAPI) serve(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(raw))

	m.mu.Lock()
	m.calls = append(m.calls, MockAPICall{Method: r.Method, Path: r.URL.Path, Body: string(raw)})
	handler, ok := m.routes[r.Method+" "+r.URL.Path]
	if !ok {
		handler = m.fallback
	}
	m.mu.Unlock()

	if handler == nil {
		http.Error(w, "no route for "+r.Method+" "+r.URL.Path, http.StatusNotFound)
		return
	}
	handler(w, r)
}

// SetRouteFunc registers a handler for the given method+path. Replaces any
// previously registered handler for the same key.
func (m *MockManagementAPI) SetRouteFunc(method, path string, h http.HandlerFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.routes[method+" "+path] = h
}

// SetHandlerFallback registers a handler invoked when no SetRoute /
// SetRouteFunc entry matches the incoming request. Without a fallback,
// unmatched requests return 404.
func (m *MockManagementAPI) SetHandlerFallback(h http.HandlerFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fallback = h
}

// SetRoute is sugar over SetRouteFunc that responds with the given status and
// a JSON-encoded payload.
func (m *MockManagementAPI) SetRoute(method, path string, status int, payload any) {
	m.SetRouteFunc(method, path, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(payload)
	})
}

// Calls returns a snapshot of every request received so far.
func (m *MockManagementAPI) Calls() []MockAPICall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]MockAPICall, len(m.calls))
	copy(out, m.calls)
	return out
}

// FindCall returns a pointer to the first recorded call matching method+path,
// or nil if none. The returned pointer references a copy; mutations do not
// affect the recorded history.
func (m *MockManagementAPI) FindCall(method, path string) *MockAPICall {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.calls {
		if c.Method == method && c.Path == path {
			out := c
			return &out
		}
	}
	return nil
}

func TestVerifyRunners(t *testing.T) {
	cmd.VerifyRunners()
}

func TestHelpOutput(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("--help")
	h.Require.NoError(err)
	h.Require.Contains(h.Stdout.String(), "Available Commands")
}

func TestOutputEnumValidation(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("api", "management", "--output", "invalid", "some/path")
	h.Require.ErrorContains(err, "must be one of")
}
