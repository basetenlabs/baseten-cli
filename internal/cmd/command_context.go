package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/auth"
	"github.com/basetenlabs/baseten-go/client"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
)

// CommandContext is passed to run functions.
type CommandContext struct {
	context.Context
	Command      *cobra.Command
	Args         []string
	JSON         bool
	JSONCompact  bool
	JSONLines    bool
	Stdin        io.Reader
	Stdout       io.Writer
	Stderr       io.Writer
	ExitWithCode func(int)

	verbose bool
}

// Output writes to stdout.
func (c *CommandContext) Output(v string) {
	panicOnOutputError(fmt.Fprint(c.Stdout, v))
}

// OutputLine writes a line to stdout.
func (c *CommandContext) OutputLine(v string) {
	panicOnOutputError(fmt.Fprintln(c.Stdout, v))
}

// Outputf writes formatted output to stdout.
func (c *CommandContext) Outputf(format string, args ...any) {
	panicOnOutputError(fmt.Fprintf(c.Stdout, format, args...))
}

// OutputJSON writes a value as JSON to stdout. Uses indentation unless
// JSONCompact is set.
func (c *CommandContext) OutputJSON(v any) {
	enc := json.NewEncoder(c.Stdout)
	if !c.JSONCompact {
		enc.SetIndent("", "  ")
	}
	panicOnOutputError(0, enc.Encode(v))
}

// NewJSONArrayWriter returns a writer that outputs a JSON array
// incrementally. Call Write for each element and Close when done.
func (c *CommandContext) NewJSONArrayWriter() *JSONArrayWriter {
	return &JSONArrayWriter{
		w:       c.Stdout,
		compact: c.JSONCompact,
		lines:   c.JSONLines,
	}
}

// Log writes to stderr.
func (c *CommandContext) Log(v string) {
	panicOnOutputError(fmt.Fprint(c.Stderr, v))
}

// LogLine writes a line to stderr.
func (c *CommandContext) LogLine(v string) {
	panicOnOutputError(fmt.Fprintln(c.Stderr, v))
}

// Logf writes formatted output to stderr.
func (c *CommandContext) Logf(format string, args ...any) {
	panicOnOutputError(fmt.Fprintf(c.Stderr, format, args...))
}

// VerboseLog writes to stderr if verbose mode is enabled.
func (c *CommandContext) VerboseLog(v string) {
	if c.verbose {
		panicOnOutputError(fmt.Fprint(c.Stderr, v))
	}
}

// VerboseLogLine writes a line to stderr if verbose mode is enabled.
func (c *CommandContext) VerboseLogLine(v string) {
	if c.verbose {
		panicOnOutputError(fmt.Fprintln(c.Stderr, v))
	}
}

// VerboseLogf writes formatted output to stderr if verbose mode is enabled.
func (c *CommandContext) VerboseLogf(format string, args ...any) {
	if c.verbose {
		panicOnOutputError(fmt.Fprintf(c.Stderr, format, args...))
	}
}

// JSONArrayWriter writes a JSON array to a writer incrementally.
type JSONArrayWriter struct {
	w       io.Writer
	compact bool
	lines   bool
	started bool
}

// Write writes a single element to the JSON array.
func (w *JSONArrayWriter) Write(v any) {
	if w.lines {
		b, err := json.Marshal(v)
		if err != nil {
			panic(err)
		}
		panicOnOutputError(fmt.Fprintf(w.w, "%s\n", b))
		return
	}
	if !w.started {
		panicOnOutputError(fmt.Fprint(w.w, "[\n"))
		w.started = true
	} else {
		panicOnOutputError(fmt.Fprint(w.w, ",\n"))
	}
	var b []byte
	var err error
	if w.compact {
		b, err = json.Marshal(v)
	} else {
		b, err = json.MarshalIndent(v, "  ", "  ")
	}
	if err != nil {
		panic(err)
	}
	panicOnOutputError(fmt.Fprintf(w.w, "  %s", b))
}

// Close finishes the JSON array.
func (w *JSONArrayWriter) Close() {
	if w.lines {
		return
	}
	if !w.started {
		panicOnOutputError(fmt.Fprint(w.w, "[]\n"))
	} else {
		panicOnOutputError(fmt.Fprint(w.w, "\n]\n"))
	}
}

func panicOnOutputError(_ any, err error) {
	if err != nil {
		panic(fmt.Sprintf("unexpected output error: %v", err))
	}
}

func (c *CommandContext) httpClient() *http.Client {
	if cl, ok := c.Value(httpClientKey{}).(*http.Client); ok {
		return cl
	}
	return http.DefaultClient
}

type httpClientKey struct{}

// WithHTTPClient returns a context that overrides the HTTP client used by
// CommandContext and therefore all SDK clients created from it.
func WithHTTPClient(ctx context.Context, c *http.Client) context.Context {
	return context.WithValue(ctx, httpClientKey{}, c)
}

const defaultHost = "https://api.baseten.co"
const oauthClientID = "baseten-cli"

// ResolveHost returns the API host from BASETEN_BASE_URL env var, or the
// default.
func ResolveHost() string {
	if h := os.Getenv("BASETEN_BASE_URL"); h != "" {
		return h
	}
	return defaultHost
}

// IsInteractive returns true if the context's stdin is a terminal.
func (c *CommandContext) IsInteractive() bool {
	f, ok := c.Stdin.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// OAuthConfig returns the OAuth2 configuration for the given host.
func OAuthConfig(host string) *oauth2.Config {
	return &oauth2.Config{
		ClientID: oauthClientID,
		Endpoint: oauth2.Endpoint{
			DeviceAuthURL: host + "/v1/users/auth/device/authorize",
			TokenURL:      host + "/v1/users/auth/device/token",
		},
	}
}

// NewAuthStore creates an auth store using the default config directory.
func NewAuthStore(insecureStorage bool) (*auth.Store, error) {
	dir, err := auth.DefaultConfigDir()
	if err != nil {
		return nil, err
	}
	return auth.NewStore(auth.StoreOptions{
		Dir:             dir,
		InsecureStorage: insecureStorage,
	}), nil
}

// NewManagementClient creates a management API client that resolves
// credentials via the auth store (env var > stored credential).
func (c *CommandContext) NewManagementClient() (*client.ManagementClient, error) {
	host := ResolveHost()
	store, err := NewAuthStore(false)
	if err != nil {
		return nil, err
	}
	transport := &auth.Transport{
		Store:       store,
		Host:        host,
		OAuthConfig: OAuthConfig(host),
		EnvAPIKey:   os.Getenv("BASETEN_API_KEY"),
		Base:        c.httpClient().Transport,
	}
	return client.NewManagementClient(client.ManagementClientOptions{
		BaseURL:    host,
		DeferAuth:  true,
		HTTPClient: transport,
	})
}

// NewManagementClientWithAuth creates a management API client with a specific
// auth header. Used during login to validate a credential before storing it.
func (c *CommandContext) NewManagementClientWithAuth(authHeader string) (*client.ManagementClient, error) {
	return client.NewManagementClient(client.ManagementClientOptions{
		BaseURL:   ResolveHost(),
		DeferAuth: true,
		HTTPClient: &staticAuthClient{
			header: authHeader,
			base:   c.httpClient(),
		},
	})
}

// NewInferenceClient creates an inference API client that resolves
// credentials via the auth store. Inference only supports API key auth.
func (c *CommandContext) NewInferenceClient(flags cmd.InferenceClientFlags) (*client.InferenceClient, error) {
	host := ResolveHost()
	store, err := NewAuthStore(false)
	if err != nil {
		return nil, err
	}
	transport := &auth.Transport{
		Store:       store,
		Host:        host,
		OAuthConfig: OAuthConfig(host),
		EnvAPIKey:   os.Getenv("BASETEN_API_KEY"),
		APIKeyOnly:  true,
		Base:        c.httpClient().Transport,
	}
	return client.NewInferenceClient(client.InferenceClientOptions{
		ModelID:     flags.ModelID,
		ChainID:     flags.ChainID,
		Environment: flags.Environment,
		BaseURL:     os.Getenv("BASETEN_INFERENCE_URL"),
		DeferAuth:   true,
		HTTPClient:  transport,
	})
}

// staticAuthClient is an HTTP client that sets a fixed Authorization header.
type staticAuthClient struct {
	header string
	base   *http.Client
}

func (c *staticAuthClient) Do(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", c.header)
	return c.base.Do(req)
}
