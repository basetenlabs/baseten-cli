package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client"
	"github.com/spf13/cobra"
)

// CommandContext is passed to run functions.
type CommandContext struct {
	context.Context
	Command      *cobra.Command
	Args         []string
	JSON         bool
	JSONCompact  bool
	JSONLines    bool
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

// NewManagementClient creates a management API client using the API key from
// BASETEN_API_KEY and base URL from BASETEN_BASE_URL (defaulting to
// https://api.baseten.co).
func (c *CommandContext) NewManagementClient() (*client.ManagementClient, error) {
	apiKey, err := GetAPIKey()
	if err != nil {
		return nil, err
	}
	return client.NewManagementClient(client.ManagementClientOptions{
		APIKey:     apiKey,
		BaseURL:    os.Getenv("BASETEN_BASE_URL"),
		HTTPClient: c.httpClient(),
	})
}

// NewInferenceClient creates an inference API client using the API key from
// BASETEN_API_KEY. The base URL is computed from the given flags, or from
// BASETEN_BASE_URL if set.
func (c *CommandContext) NewInferenceClient(flags cmd.InferenceClientFlags) (*client.InferenceClient, error) {
	apiKey, err := GetAPIKey()
	if err != nil {
		return nil, err
	}
	return client.NewInferenceClient(client.InferenceClientOptions{
		APIKey:      apiKey,
		ModelID:     flags.ModelID,
		ChainID:     flags.ChainID,
		Environment: flags.Environment,
		BaseURL:     os.Getenv("BASETEN_BASE_URL"),
		HTTPClient:  c.httpClient(),
	})
}
