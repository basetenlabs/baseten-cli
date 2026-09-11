package cmd

import (
	"encoding/json"
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

// apiErrorBody is the union of the error body shapes our APIs return. The
// management API sends {"code", "message", "details"}, the inference API
// sends {"error", "error_code", "detail"}, and a failure reported by an
// upstream proxy rather than the application sends only a message field.
// Absent fields decode to their zero value, so one struct covers all of them.
type apiErrorBody struct {
	Code      string         `json:"code"`
	ErrorCode string         `json:"error_code"`
	Details   map[string]any `json:"details"`
}

// apiErrorFields extracts the api_* fields of the JSON error envelope from a
// recognized HTTP client error in err's chain. Returns a zero JSONError if
// the failure did not come from an API call: the fields are omitempty, so a
// local failure carries none of them.
//
// Only the status code is guaranteed. A body that isn't JSON, or that carries
// no code or details, contributes nothing rather than failing: the message
// already relays whatever the response said.
func apiErrorFields(err error) cmd.JSONError {
	status, ok := knownHTTPStatus(err)
	if !ok {
		return cmd.JSONError{}
	}
	out := cmd.JSONError{APIStatusCode: status}

	// The inference API's typed error is already decoded. Everything else
	// carries a raw body to parse.
	var irer *inferenceapi.ResponseErrorResponse
	if errors.As(err, &irer) {
		if code := irer.ErrorResponse.ErrorCode; code != nil {
			out.APIErrorCode = string(*code)
		}
		return out
	}

	var rawBody string
	var mre *managementapi.ResponseError
	var ire *inferenceapi.ResponseError
	switch {
	case errors.As(err, &mre):
		rawBody = mre.Body
	case errors.As(err, &ire):
		rawBody = ire.Body
	}
	// The decode error is deliberately ignored. Unknown and absent fields are
	// not errors, so the only failures are a body that isn't a JSON object at
	// all or a field of an unexpected type, and both still populate whatever
	// did decode. Anything we can't read contributes nothing, since the
	// message already relays the response verbatim.
	var body apiErrorBody
	_ = json.Unmarshal([]byte(rawBody), &body)
	out.APIErrorCode = body.Code
	if out.APIErrorCode == "" {
		out.APIErrorCode = body.ErrorCode
	}
	out.APIDetails = body.Details
	return out
}
