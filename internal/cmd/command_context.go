package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/auth"
	"github.com/basetenlabs/baseten-go/client"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/itchyny/gojq"
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
	// JQQuery is a compiled --jq expression installed by the framework. When
	// non-nil, [OutputJSON] and [JSONArrayWriter.Write] route their input
	// through the query before encoding. Leaves should not set this directly.
	JQQuery *gojq.Query

	verbose bool
	jqErr   error

	// authInfo lazily resolves the remote and auth session. Use the Remote and
	// Session accessors; do not read its cached fields directly.
	authInfo authInfo
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
// JSONCompact is set. When [CommandContext.JQQuery] is set, the value is
// routed through the jq query and each result is emitted in turn; a jq
// runtime error is stashed on the context and surfaced by the framework
// after the runner returns.
func (c *CommandContext) OutputJSON(v any) {
	if c.jqErr != nil {
		return
	}
	if c.JQQuery != nil {
		normalized, err := normalizeForJQ(v)
		if err != nil {
			c.jqErr = err
			return
		}
		iter := c.JQQuery.Run(normalized)
		for {
			res, ok := iter.Next()
			if !ok {
				return
			}
			if err, isErr := res.(error); isErr {
				c.jqErr = fmt.Errorf("jq error: %w", err)
				return
			}
			c.encodeJSON(res)
		}
	}
	c.encodeJSON(v)
}

// normalizeForJQ round-trips v through JSON so struct values become the
// map/slice primitives gojq expects.
func normalizeForJQ(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("normalizing value for jq: %w", err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("normalizing value for jq: %w", err)
	}
	return out, nil
}

func (c *CommandContext) encodeJSON(v any) {
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
		ctx:     c,
		w:       c.Stdout,
		compact: c.JSONCompact,
		lines:   c.JSONLines,
	}
}

// TableOutput describes a borderless table for OutputTable.
type TableOutput struct {
	Headers []string
	Rows    [][]string
	// RightAlignedColumns lists column indices whose header and cells are
	// right-aligned; columns absent from the list default to left-aligned.
	RightAlignedColumns []int
}

// OutputTable writes a borderless table to stdout with bold headers. Header
// styling auto-degrades when stdout is not a terminal.
func (c *CommandContext) OutputTable(out TableOutput) {
	renderer := lipgloss.NewRenderer(c.Stdout)
	rightAligned := make(map[int]bool, len(out.RightAlignedColumns))
	for _, col := range out.RightAlignedColumns {
		rightAligned[col] = true
	}
	headerStyle := renderer.NewStyle().Bold(true).PaddingRight(2)
	cellStyle := renderer.NewStyle().PaddingRight(2)
	t := table.New().
		Border(lipgloss.HiddenBorder()).
		BorderTop(false).
		BorderBottom(false).
		BorderLeft(false).
		BorderRight(false).
		BorderHeader(false).
		BorderColumn(false).
		BorderRow(false).
		Headers(out.Headers...).
		Rows(out.Rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			style := cellStyle
			if row == table.HeaderRow {
				style = headerStyle
			}
			if rightAligned[col] {
				style = style.Align(lipgloss.Right)
			}
			return style
		})
	panicOnOutputError(fmt.Fprintln(c.Stdout, t.Render()))
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
	ctx     *CommandContext
	w       io.Writer
	compact bool
	lines   bool
	started bool
}

// Write writes a single element to the JSON array. When the owning context
// has [CommandContext.JQQuery] set, the element is routed through the query
// and each result is emitted as its own record; runtime errors are stashed
// on the context and surfaced by the framework.
func (w *JSONArrayWriter) Write(v any) {
	if w.ctx.jqErr != nil {
		return
	}
	if w.ctx.JQQuery != nil {
		normalized, err := normalizeForJQ(v)
		if err != nil {
			w.ctx.jqErr = err
			return
		}
		iter := w.ctx.JQQuery.Run(normalized)
		for {
			res, ok := iter.Next()
			if !ok {
				return
			}
			if err, isErr := res.(error); isErr {
				w.ctx.jqErr = fmt.Errorf("jq error: %w", err)
				return
			}
			w.writeOne(res)
		}
	}
	w.writeOne(v)
}

func (w *JSONArrayWriter) writeOne(v any) {
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

const oauthClientID = "baseten-cli"

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

// ConfirmYesNo prompts the user with a yes/no question. Returns an ErrUsage
// when stdin is not a terminal so callers can instruct the user to pass --yes
// or similar. Returns a non-nil error if the user declines.
func (c *CommandContext) ConfirmYesNo(title string) error {
	if !c.IsInteractive() {
		return cmd.NewErrUsagef("cannot confirm: stdin is not a terminal; pass --yes to skip the prompt")
	}
	var ok bool
	if err := huh.NewConfirm().Title(title).Value(&ok).Run(); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("aborted")
	}
	return nil
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

// SetDefaultProfile sets a fallback profile, used only when no profile is
// selected via --profile, BASETEN_API_KEY, or BASETEN_PROFILE. It must be
// called before anything that resolves auth (AuthTransport, NewManagementClient,
// NewInferenceClient, or the Remote/Session accessors): the session resolves
// once and is cached, so a call afterward has no effect.
func (c *CommandContext) SetDefaultProfile(name string) {
	c.authInfo.defaultProfile = name
}

// AuthTransport builds an HTTP transport that injects the active session's
// credential on every request, regardless of the request host. Shared by the
// SDK clients and by commands that POST to non-SDK hosts (e.g. a Model API URL).
// The resolved remote is returned alongside so callers can derive URLs without
// resolving it again.
func (c *CommandContext) AuthTransport() (*auth.Transport, *Remote, error) {
	remote, err := c.authInfo.Remote()
	if err != nil {
		return nil, nil, err
	}
	session, err := c.authInfo.Session()
	if err != nil {
		return nil, nil, err
	}
	return &auth.Transport{
		Session:     session,
		OAuthConfig: OAuthConfig(remote.ManagementURL()),
		Base:        c.httpClient().Transport,
	}, remote, nil
}

// NewManagementClient creates a management API client that resolves
// credentials via the auth store (env var > stored credential).
func (c *CommandContext) NewManagementClient() (*client.ManagementClient, error) {
	transport, remote, err := c.AuthTransport()
	if err != nil {
		return nil, err
	}
	return client.NewManagementClient(client.ManagementClientOptions{
		BaseURL:    remote.ManagementURL(),
		DeferAuth:  true,
		HTTPClient: transport,
	})
}

// NewManagementClientWithAuth creates a management API client against mgmtURL
// with a specific auth header. Used during login to validate a credential
// before storing it, before any profile exists.
func (c *CommandContext) NewManagementClientWithAuth(mgmtURL, authHeader string) (*client.ManagementClient, error) {
	return client.NewManagementClient(client.ManagementClientOptions{
		BaseURL:   mgmtURL,
		DeferAuth: true,
		HTTPClient: &staticAuthClient{
			header: authHeader,
			base:   c.httpClient(),
		},
	})
}

// NewInferenceClient creates an inference API client that resolves
// credentials via the auth store.
func (c *CommandContext) NewInferenceClient(flags cmd.InferenceClientFlags) (*client.InferenceClient, error) {
	transport, remote, err := c.AuthTransport()
	if err != nil {
		return nil, err
	}
	baseURL, err := remote.InferenceBaseURL(flags.ModelID, flags.ChainID, flags.Environment)
	if err != nil {
		return nil, err
	}
	hostHeader, hostOverride, err := remote.InferenceHostHeader(flags.ModelID, flags.ChainID, flags.Environment)
	if err != nil {
		return nil, err
	}
	opts := client.InferenceClientOptions{
		BaseURL:    baseURL,
		DeferAuth:  true,
		HTTPClient: transport,
	}
	if hostOverride {
		opts.HTTPClient = &hostHeaderClient{host: hostHeader, base: transport}
	}
	return client.NewInferenceClient(opts)
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

// Now returns the current wall-clock time, honoring any override installed
// via WithNow. Used by command runners for any "now" calculation so tests
// can pin the clock.
func (c *CommandContext) Now() time.Time {
	if fn, ok := c.Value(nowKey{}).(func() time.Time); ok {
		return fn()
	}
	return time.Now()
}

// Sleep pauses for d, honoring any override installed via WithSleep and
// returning early with ctx.Err() if the context is cancelled.
func (c *CommandContext) Sleep(d time.Duration) error {
	if fn, ok := c.Value(sleepKey{}).(func(context.Context, time.Duration) error); ok {
		return fn(c, d)
	}
	select {
	case <-c.Done():
		return c.Err()
	case <-time.After(d):
		return nil
	}
}

type nowKey struct{}
type sleepKey struct{}

// WithNow returns a context that pins CommandContext.Now to fn. Intended for
// tests; production code uses time.Now via the default path.
func WithNow(ctx context.Context, fn func() time.Time) context.Context {
	return context.WithValue(ctx, nowKey{}, fn)
}

// WithSleep returns a context that intercepts CommandContext.Sleep with fn.
// Intended for tests so polling loops complete instantly.
func WithSleep(ctx context.Context, fn func(context.Context, time.Duration) error) context.Context {
	return context.WithValue(ctx, sleepKey{}, fn)
}

// S3APIClientFactory builds an S3 client from a fully-populated aws.Config
// (region + credentials). Tests can inject a fake to capture upload calls.
type S3APIClientFactory func(aws.Config) transfermanager.S3APIClient

type s3FactoryKey struct{}

// WithS3APIClientFactory returns a context that overrides how
// CommandContext.NewS3APIClient builds the S3 client used for archive uploads.
func WithS3APIClientFactory(ctx context.Context, f S3APIClientFactory) context.Context {
	return context.WithValue(ctx, s3FactoryKey{}, f)
}

// newS3APIClient builds the S3 client used for archive uploads. The default
// is s3.NewFromConfig; tests can substitute a fake via WithS3APIClientFactory.
func (c *CommandContext) newS3APIClient(cfg aws.Config) transfermanager.S3APIClient {
	if f, ok := c.Value(s3FactoryKey{}).(S3APIClientFactory); ok {
		return f(cfg)
	}
	return s3.NewFromConfig(cfg)
}

// Execer looks up and runs external commands. The default uses os/exec; tests
// inject a fake via WithExecer to avoid spawning real processes.
type Execer interface {
	// LookPath reports whether name is an executable on PATH, like exec.LookPath.
	LookPath(name string) (string, error)
	// Exec runs cmd (already built via exec.CommandContext, so it carries the
	// command context and wired stdio/env) and returns an [ErrSubprocess] on a
	// non-zero exit so the inner exit code propagates.
	Exec(cmd *exec.Cmd) error
}

// defaultExecer is the production [Execer] backed by os/exec.
type defaultExecer struct{}

func (defaultExecer) LookPath(name string) (string, error) { return exec.LookPath(name) }

func (defaultExecer) Exec(cmd *exec.Cmd) error {
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return &ErrSubprocess{Err: err, Code: exitErr.ExitCode()}
		}
		return err
	}
	return nil
}

type execerKey struct{}

// WithExecer returns a context that overrides the [Execer] used by
// CommandContext to look up and run external commands. Intended for tests.
func WithExecer(ctx context.Context, e Execer) context.Context {
	return context.WithValue(ctx, execerKey{}, e)
}

// Execer returns the [Execer] used to run external commands, honoring any
// override installed via [WithExecer].
func (c *CommandContext) Execer() Execer {
	if e, ok := c.Value(execerKey{}).(Execer); ok {
		return e
	}
	return defaultExecer{}
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

// hostHeaderClient wraps an HTTP client to force a specific Host header on
// outgoing requests. Used when the remote requires a Host header that does
// not match the request URL's host.
type hostHeaderClient struct {
	host string
	base interface {
		Do(*http.Request) (*http.Response, error)
	}
}

func (c *hostHeaderClient) Do(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Host = c.host
	return c.base.Do(req)
}

// authInfo lazily resolves and caches the remote and auth session for one
// invocation. Because the selected profile carries the remote URL, both are
// resolved together by resolve (guarded by once). The remote and session
// fields are caches; callers use the Remote and Session accessors.
type authInfo struct {
	profileFlag    string
	defaultProfile string

	once    sync.Once
	err     error
	remote  *Remote
	session *auth.Session
}

func (a *authInfo) resolve() error {
	a.once.Do(func() {
		if a.session, a.err = auth.ResolveSession(a.profileFlag, a.defaultProfile); a.err != nil {
			return
		}
		a.remote, a.err = NewRemote(a.session.RemoteURL())
	})
	return a.err
}

// Remote returns the remote for this invocation, derived from the selected
// profile (or ephemeral env credentials, or the default).
func (a *authInfo) Remote() (*Remote, error) {
	if err := a.resolve(); err != nil {
		return nil, err
	}
	return a.remote, nil
}

// Session returns the auth session for this invocation.
func (a *authInfo) Session() (*auth.Session, error) {
	if err := a.resolve(); err != nil {
		return nil, err
	}
	return a.session, nil
}
