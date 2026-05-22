package cmd

import (
	"errors"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/inferenceapi"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

// ErrSubprocess carries a raw process exit code. Used for subprocess
// passthrough (e.g. truss) where we want the inner exit code verbatim,
// bypassing the standard typed-error classification.
type ErrSubprocess struct {
	Err  error
	Code int
}

func (e *ErrSubprocess) Error() string          { return e.Err.Error() }
func (e *ErrSubprocess) Unwrap() error          { return e.Err }
func (e *ErrSubprocess) ExitCode() cmd.ExitCode { return cmd.ExitCode(e.Code) }
func (*ErrSubprocess) Meaning() string          { return "Subprocess exit code" }

// normalizeError turns any returned error into a [cmd.CommandError]: raw
// HTTP client errors become the matching typed error via [cmd.WrapHTTPStatus],
// anything else falls back to [cmd.ErrGeneric]. Returns nil on a nil input.
func normalizeError(err error) cmd.CommandError {
	if err == nil {
		return nil
	}
	var ce cmd.CommandError
	if errors.As(err, &ce) {
		return ce
	}
	if status, ok := knownHTTPStatus(err); ok {
		return cmd.WrapHTTPStatus(status, err)
	}
	return cmd.NewErrGeneric(err)
}

// knownHTTPStatus extracts a status code from a recognized HTTP client error
// type in the chain. Returns false if none is present.
func knownHTTPStatus(err error) (int, bool) {
	var mre *managementapi.ResponseError
	if errors.As(err, &mre) {
		return mre.StatusCode, true
	}
	var ire *inferenceapi.ResponseError
	if errors.As(err, &ire) {
		return ire.StatusCode, true
	}
	var irer *inferenceapi.ResponseErrorResponse
	if errors.As(err, &irer) {
		return irer.StatusCode, true
	}
	return 0, false
}
