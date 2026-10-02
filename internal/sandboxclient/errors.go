package sandboxclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/basetenlabs/baseten-go/client/managementapi"
	"github.com/basetenlabs/baseten-go/client/sandboxapi"
)

var errAPIKeyWithProvider = errors.New("APIKey must be empty when TokenProvider is set")

// SandboxAPIError is an error response from the sandbox control plane or from
// a sandbox.
//
// Body holds the response body, parsed as JSON when possible, otherwise the
// raw text. Code holds the machine-readable error code when the response has
// one; the control plane puts it in "code" or "error", a sandbox in "error".
type SandboxAPIError struct {
	Status  int
	Code    string
	Details any
	Body    any
}

func (e *SandboxAPIError) Error() string {
	fields, _ := e.Body.(map[string]any)
	description := ""
	if message, ok := fields["message"].(string); ok {
		description = message
	} else if errText, ok := fields["error"].(string); ok {
		description = errText
	}
	message := fmt.Sprintf("sandbox API error (HTTP %d)", e.Status)
	if e.Code != "" {
		message += " " + e.Code
	}
	if description != "" {
		message += ": " + description
	}
	return message
}

// SandboxGatewayError is a 502, 503, or 504 from the edge in front of a
// sandbox rather than from the sandbox itself. Usually transient, for example
// while a sandbox wakes from standby or when a request outlasts the edge's
// timeout.
type SandboxGatewayError struct {
	SandboxAPIError
}

// Gateway statuses come from the edge in front of a sandbox. The control
// plane does not sit behind one, so the same statuses from it stay ordinary
// errors.
var gatewayStatuses = map[int]bool{http.StatusBadGateway: true, http.StatusServiceUnavailable: true, http.StatusGatewayTimeout: true}

// toSandboxAPIError converts an error from a generated client into a
// *SandboxAPIError. Anything else, such as a network failure, is returned
// unchanged.
//
// plane is "control" or "exec", deciding whether gateway statuses become
// SandboxGatewayError.
func toSandboxAPIError(err error, plane string) error {
	var body any
	var status int
	var managementResponseError *managementapi.ResponseError
	var sandboxResponseError *sandboxapi.ResponseError
	var typedErrorResponse *sandboxapi.ResponseErrorResponse
	switch {
	case errors.As(err, &managementResponseError):
		status = managementResponseError.StatusCode
		body = parseErrorBody(managementResponseError.Body)
	case errors.As(err, &sandboxResponseError):
		status = sandboxResponseError.StatusCode
		body = parseErrorBody(sandboxResponseError.Body)
	case errors.As(err, &typedErrorResponse):
		status = typedErrorResponse.StatusCode
		// The typed error model carries only "error"; the same shape as the
		// parsed body gives code extraction one contract.
		raw, _ := json.Marshal(typedErrorResponse.ErrorResponse)
		body = parseErrorBody(string(raw))
	default:
		return err
	}
	if plane == "exec" && gatewayStatuses[status] {
		return &SandboxGatewayError{SandboxAPIError: sandboxAPIError(status, body)}
	}
	return sandboxAPIErrorPtr(status, body)
}

func sandboxAPIErrorPtr(status int, body any) *SandboxAPIError {
	apiError := sandboxAPIError(status, body)
	return &apiError
}

func sandboxAPIError(status int, body any) SandboxAPIError {
	fields, _ := body.(map[string]any)
	code, _ := fields["code"].(string)
	if code == "" {
		// The control plane puts the code in "error" and the description in
		// "message". A sandbox puts the description in "error".
		if _, hasMessage := fields["message"].(string); hasMessage {
			code, _ = fields["error"].(string)
		}
	}
	var details any
	if d, ok := fields["details"]; ok {
		details = d
	}
	return SandboxAPIError{Status: status, Code: code, Details: details, Body: body}
}

func parseErrorBody(body string) any {
	var parsed any
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return body
	}
	return parsed
}
