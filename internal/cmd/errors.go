package cmd

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/inferenceapi"
	"github.com/basetenlabs/baseten-go/client/managementapi"
	"github.com/basetenlabs/baseten-go/client/sandboxapi"
	"github.com/basetenlabs/baseten-go/sandbox"
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
	// A sandbox's own API, through the high-level client (whose gateway
	// errors unwrap to this too) or the generated one.
	var sae *sandbox.APIError
	if errors.As(err, &sae) {
		return sae.Status, true
	}
	var sre *sandboxapi.ResponseError
	if errors.As(err, &sre) {
		return sre.StatusCode, true
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
	// The sandbox client's error is decoded already too.
	var sae *sandbox.APIError
	if errors.As(err, &sae) {
		out.APIErrorCode = sae.Code
		if details, ok := sae.Details.(map[string]any); ok {
			out.APIDetails = details
		}
		return out
	}

	var rawBody string
	var mre *managementapi.ResponseError
	var ire *inferenceapi.ResponseError
	var sre *sandboxapi.ResponseError
	switch {
	case errors.As(err, &mre):
		rawBody = mre.Body
	case errors.As(err, &ire):
		rawBody = ire.Body
	case errors.As(err, &sre):
		rawBody = sre.Body
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

// apiErrorText renders a failure from an API response for stderr. The raw
// body escapes the message's newlines and buries it in JSON, so the message
// is shown as text after the status and code. Returns false when err did not
// come from an API response, or its body has no message, so the caller prints
// the error as is.
func apiErrorText(err error, verbose bool) (string, bool) {
	status, apiText, rawBody, ok := apiResponse(err)
	if !ok {
		return "", false
	}
	// Commands wrap the API error with what they were doing, which is kept.
	prefix, found := strings.CutSuffix(err.Error(), apiText)
	if !found {
		return "", false
	}
	var body struct {
		apiErrorBody
		Message string `json:"message"`
		// The inference API and upstream proxies put the message under
		// "error" or "detail". FastAPI's validation detail is a list, so
		// these stay untyped and only a string is used.
		Error  any `json:"error"`
		Detail any `json:"detail"`
	}
	_ = json.Unmarshal([]byte(rawBody), &body)
	message := body.Message
	for _, alt := range []any{body.Error, body.Detail} {
		if s, isString := alt.(string); message == "" && isString {
			message = s
		}
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return "", false
	}
	statusText := fmt.Sprintf("HTTP %d", status)
	if code := cmp.Or(body.Code, body.ErrorCode); code != "" {
		statusText += ", " + code
	}
	var b strings.Builder
	if !strings.Contains(message, "\n") {
		fmt.Fprintf(&b, "%s%s (%s)", prefix, message, statusText)
	} else {
		fmt.Fprintf(&b, "%srequest failed (%s)\n", prefix, statusText)
		for line := range strings.Lines(message) {
			b.WriteString("\n  " + strings.TrimRight(line, "\r\n"))
		}
	}
	if verbose {
		b.WriteString("\n\n  response body: " + rawBody)
	}
	return b.String(), true
}

// apiResponse finds a failed API response in err's chain and returns its
// status, the error text the client formatted for it, and its body.
func apiResponse(err error) (status int, text, body string, ok bool) {
	var mre *managementapi.ResponseError
	var ire *inferenceapi.ResponseError
	var irer *inferenceapi.ResponseErrorResponse
	var sre *sandboxapi.ResponseError
	switch {
	case errors.As(err, &mre):
		return mre.StatusCode, mre.Error(), mre.Body, true
	case errors.As(err, &ire):
		return ire.StatusCode, ire.Error(), ire.Body, true
	case errors.As(err, &irer):
		decoded, marshalErr := json.Marshal(irer.ErrorResponse)
		if marshalErr != nil {
			return 0, "", "", false
		}
		return irer.StatusCode, irer.Error(), string(decoded), true
	case errors.As(err, &sre):
		return sre.StatusCode, sre.Error(), sre.Body, true
	}
	return 0, "", "", false
}
