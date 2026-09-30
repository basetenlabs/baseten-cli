package cmd

import (
	"fmt"
	"reflect"
)

// ExitCode is the process exit code the CLI returns. The standard set below
// is what the framework emits automatically; commands may declare additional
// codes by implementing [CommandError] on a typed error and listing it in
// [Command.Errors]. The enum is open: codes beyond [ExitServer] are reserved
// for command-declared errors.
type ExitCode int

const (
	ExitSuccess    ExitCode = 0
	ExitGeneric    ExitCode = 1
	ExitUsage      ExitCode = 2
	ExitAuth       ExitCode = 3
	ExitNotFound   ExitCode = 4
	ExitValidation ExitCode = 5
	ExitServer     ExitCode = 6
	// ExitInterrupted is returned when the command's context is cancelled
	// (Ctrl-C / SIGTERM). 128 + SIGINT(2), the conventional shell convention.
	ExitInterrupted ExitCode = 130
)

// JSONErrorEnvelope is written to stdout when a command fails under
// --output json or --output jsonl, on top of the plain-text message that goes
// to stderr whatever the output format.
//
// It is the last JSON document on stdout: alone when the command produced no
// payload, or after the records a streamed command emitted. Two kinds of
// failure write none. A command whose own payload already reports the failure
// suppresses it, so stdout stays a single document under --output json, and a
// command that delegates to another tool leaves that tool's stdout untouched.
type JSONErrorEnvelope struct {
	Error JSONError `json:"error"`
}

// JSONError is the error object inside a [JSONErrorEnvelope].
type JSONError struct {
	// Message is the human-readable failure message, identical to the line
	// written to stderr. For a failure originating in an API response this
	// includes the raw response body.
	Message string `json:"message"`
	// Type is the name of the typed error, matching the names listed in the
	// exit-code tables of `baseten --help-output` and each command's
	// --help-output.
	Type string `json:"type"`
	// ExitCode is the process exit code, the same value the shell sees.
	ExitCode ExitCode `json:"exit_code"`
	// APIStatusCode is the HTTP status of the failed API response. Absent when
	// the failure did not come from an API call.
	APIStatusCode int `json:"api_status_code,omitempty"`
	// APIErrorCode is the machine-readable error code from the API response
	// body. Absent when the failure did not come from an API call, or when the
	// response carried no code (some failures are reported by an upstream
	// proxy whose body has only a message).
	APIErrorCode string `json:"api_error_code,omitempty"`
	// APIDetails is the structured, per-failure-kind payload from the API
	// response body. Absent when the failure did not come from an API call, or
	// when the response carried no details.
	APIDetails map[string]any `json:"api_details,omitempty"`
}

// CommandError is implemented by typed errors a command may return. The
// framework calls [errors.As] on the returned error to discover a
// CommandError implementation and uses its [ExitCode] and [Meaning] to
// classify the failure and populate the [JSONErrorEnvelope].
//
// Implementations should embed [CommandErrorMeta] for the underlying
// error/unwrap plumbing and provide static [ExitCode] and [Meaning] on a
// pointer receiver so they can be queried without an instance.
type CommandError interface {
	error
	ExitCode() ExitCode
	Meaning() string
}

// CommandErrorMeta is embedded by typed [CommandError] implementations to
// supply [error.Error] and [errors.Unwrap]. Construct via [NewCommandErrorMeta].
type CommandErrorMeta struct {
	msg     string
	wrapped error
}

// NewCommandErrorMeta builds a meta with the formatted message and optional
// wrapped error.
func NewCommandErrorMeta(msg string, wrapped error) CommandErrorMeta {
	return CommandErrorMeta{msg: msg, wrapped: wrapped}
}

func (m *CommandErrorMeta) Error() string { return m.msg }
func (m *CommandErrorMeta) Unwrap() error { return m.wrapped }

// ErrorDesc documents one command-declared error: its Go type name, the
// [ExitCode] it surfaces, and the static [CommandError.Meaning]. Listed in
// [Command.Errors] and rendered in --help-output.
type ErrorDesc struct {
	Name    string
	Code    ExitCode
	Meaning string
}

// ErrorDescOf builds an [ErrorDesc] from a typed command error. Call as
// ErrorDescOf[*ErrFoo](). Panics if the error reports [ExitCode] == 0
// (success), which is meaningless for an error.
func ErrorDescOf[PT CommandError]() ErrorDesc {
	pt := reflect.TypeFor[PT]()
	if pt.Kind() != reflect.Pointer {
		panic(fmt.Sprintf("ErrorDescOf requires a pointer type, got %v", pt))
	}
	inst := reflect.New(pt.Elem()).Interface().(PT)
	code := inst.ExitCode()
	if code == ExitSuccess {
		panic(fmt.Sprintf("typed command error %s has ExitCode() == 0", pt.Elem().Name()))
	}
	return ErrorDesc{Name: ErrorTypeName(inst), Code: code, Meaning: inst.Meaning()}
}

// ErrGeneric is the catch-all typed error for failures with no more specific
// classification. Surfaces [ExitGeneric].
type ErrGeneric struct{ CommandErrorMeta }

func (*ErrGeneric) ExitCode() ExitCode { return ExitGeneric }
func (*ErrGeneric) Meaning() string    { return "Unspecified error" }

// NewErrGeneric wraps any error as an [ErrGeneric]. Returns nil if err is nil.
func NewErrGeneric(err error) *ErrGeneric { return wrapErr[ErrGeneric](err) }

// ErrUsage signals invalid command invocation (bad flags, missing required
// input, mutually exclusive options). Surfaces [ExitUsage] and tells the
// framework to display the command's usage line alongside the error.
type ErrUsage struct{ CommandErrorMeta }

func (*ErrUsage) ExitCode() ExitCode { return ExitUsage }
func (*ErrUsage) Meaning() string    { return "Invalid usage" }

// NewErrUsage wraps any error as an [ErrUsage].
func NewErrUsage(err error) *ErrUsage { return wrapErr[ErrUsage](err) }

// NewErrUsagef builds an [ErrUsage] from a [fmt.Errorf]-style format string.
// Supports %w to wrap an underlying error.
func NewErrUsagef(format string, args ...any) *ErrUsage {
	return NewErrUsage(fmt.Errorf(format, args...))
}

// ErrAuth signals an authentication or authorization failure (typically HTTP
// 401/403). Surfaces [ExitAuth].
type ErrAuth struct{ CommandErrorMeta }

func (*ErrAuth) ExitCode() ExitCode { return ExitAuth }
func (*ErrAuth) Meaning() string    { return "Authentication failed" }

// NewErrAuth wraps any error as an [ErrAuth].
func NewErrAuth(err error) *ErrAuth { return wrapErr[ErrAuth](err) }

// ErrNotFound signals that a referenced resource does not exist (typically
// HTTP 404). Surfaces [ExitNotFound].
type ErrNotFound struct{ CommandErrorMeta }

func (*ErrNotFound) ExitCode() ExitCode { return ExitNotFound }
func (*ErrNotFound) Meaning() string    { return "Resource not found" }

// NewErrNotFound wraps any error as an [ErrNotFound].
func NewErrNotFound(err error) *ErrNotFound { return wrapErr[ErrNotFound](err) }

// ErrValidation signals a request rejected by server-side validation
// (typically HTTP 4xx other than 401/403/404). Surfaces [ExitValidation].
type ErrValidation struct{ CommandErrorMeta }

func (*ErrValidation) ExitCode() ExitCode { return ExitValidation }
func (*ErrValidation) Meaning() string    { return "Request validation failed" }

// NewErrValidation wraps any error as an [ErrValidation].
func NewErrValidation(err error) *ErrValidation { return wrapErr[ErrValidation](err) }

// ErrServer signals a server-side failure (typically HTTP 5xx). Surfaces
// [ExitServer].
type ErrServer struct{ CommandErrorMeta }

func (*ErrServer) ExitCode() ExitCode { return ExitServer }
func (*ErrServer) Meaning() string    { return "Server error" }

// NewErrServer wraps any error as an [ErrServer].
func NewErrServer(err error) *ErrServer { return wrapErr[ErrServer](err) }

// ErrInterrupted signals that the command's context was cancelled by a signal
// (Ctrl-C / SIGTERM) rather than failing on its own. Surfaces
// [ExitInterrupted].
type ErrInterrupted struct{ CommandErrorMeta }

func (*ErrInterrupted) ExitCode() ExitCode { return ExitInterrupted }
func (*ErrInterrupted) Meaning() string    { return "Interrupted by signal" }

// NewErrInterrupted wraps any error as an [ErrInterrupted].
func NewErrInterrupted(err error) *ErrInterrupted { return wrapErr[ErrInterrupted](err) }

// ErrorTypeName returns the name of a [CommandError]'s concrete type: the
// value carried in [JSONError.Type], and the name listed alongside the exit
// code in the --help-output exit-code tables.
func ErrorTypeName(err CommandError) string {
	t := reflect.TypeOf(err)
	if t == nil {
		return ""
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.Name()
}

// hasCommandErrorMeta is the constraint on the typed-error structs that embed
// [CommandErrorMeta], used by [wrapErr] to inject the meta into a fresh value.
type hasCommandErrorMeta interface {
	setCommandErrorMeta(CommandErrorMeta)
}

func (e *ErrGeneric) setCommandErrorMeta(m CommandErrorMeta)     { e.CommandErrorMeta = m }
func (e *ErrUsage) setCommandErrorMeta(m CommandErrorMeta)       { e.CommandErrorMeta = m }
func (e *ErrAuth) setCommandErrorMeta(m CommandErrorMeta)        { e.CommandErrorMeta = m }
func (e *ErrNotFound) setCommandErrorMeta(m CommandErrorMeta)    { e.CommandErrorMeta = m }
func (e *ErrValidation) setCommandErrorMeta(m CommandErrorMeta)  { e.CommandErrorMeta = m }
func (e *ErrServer) setCommandErrorMeta(m CommandErrorMeta)      { e.CommandErrorMeta = m }
func (e *ErrInterrupted) setCommandErrorMeta(m CommandErrorMeta) { e.CommandErrorMeta = m }

// wrapErr is the shared constructor for typed command errors. Returns nil if
// err is nil so callers can `return cmd.NewErrXxx(maybeNil)` without a guard.
func wrapErr[T any, PT interface {
	*T
	hasCommandErrorMeta
}](err error) PT {
	if err == nil {
		return nil
	}
	out := PT(new(T))
	out.setCommandErrorMeta(NewCommandErrorMeta(err.Error(), err))
	return out
}

// StandardErrors enumerates the framework-provided typed [CommandError]s in
// the order the root command's --help-output should render them. Each leaf
// command inherits these implicitly; per-leaf [Command.Errors] only lists
// errors *beyond* this standard set.
func StandardErrors() []ErrorDesc {
	return []ErrorDesc{
		ErrorDescOf[*ErrGeneric](),
		ErrorDescOf[*ErrUsage](),
		ErrorDescOf[*ErrAuth](),
		ErrorDescOf[*ErrNotFound](),
		ErrorDescOf[*ErrValidation](),
		ErrorDescOf[*ErrServer](),
		ErrorDescOf[*ErrInterrupted](),
	}
}

// WrapHTTPStatus picks the appropriate standard typed error for an HTTP
// status code and wraps the underlying error. Status → error: 401/403 →
// [ErrAuth], 404 → [ErrNotFound], other 4xx → [ErrValidation], 5xx →
// [ErrServer], anything else → [ErrGeneric].
func WrapHTTPStatus(status int, err error) CommandError {
	switch {
	case status == 401 || status == 403:
		return NewErrAuth(err)
	case status == 404:
		return NewErrNotFound(err)
	case status >= 400 && status < 500:
		return NewErrValidation(err)
	case status >= 500:
		return NewErrServer(err)
	}
	return NewErrGeneric(err)
}
