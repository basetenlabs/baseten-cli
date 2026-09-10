package cmd_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/basetenlabs/baseten-cli/cmd"
)

const billingUsagePath = "/v1/billing/usage_summary"

// decodeJSONErrorEnvelope decodes the error envelope a failed command writes
// to stdout, asserting stdout holds exactly that one document.
func decodeJSONErrorEnvelope(h *CommandHarness) cmd.JSONError {
	h.T.Helper()
	var envelope cmd.JSONErrorEnvelope
	dec := json.NewDecoder(strings.NewReader(h.Stdout.String()))
	h.Require.NoError(dec.Decode(&envelope))
	h.Require.False(dec.More(), "expected a single JSON document on stdout")
	return envelope.Error
}

func TestJSONErrorManagementAPIErrorCarriesCodeAndDetails(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", billingUsagePath, 409, map[string]any{
		"code":    "CONFLICT",
		"message": "Usage summary is being rebuilt",
		"details": map[string]any{"retry_after_sec": 30},
	})

	h.Require.Error(h.Execute("org", "billing", "usage", "--output", "json"))
	h.Require.Equal(int(cmd.ExitValidation), h.ExitCode)
	// The same message goes to stderr whatever the output format.
	h.Require.Contains(h.Stderr.String(), "Usage summary is being rebuilt")

	jsonErr := decodeJSONErrorEnvelope(h)
	h.Require.Contains(jsonErr.Message, "Usage summary is being rebuilt")
	h.Require.Equal("ErrValidation", jsonErr.Type)
	h.Require.Equal(cmd.ExitValidation, jsonErr.ExitCode)
	h.Require.Equal(409, jsonErr.APIStatusCode)
	h.Require.Equal("CONFLICT", jsonErr.APIErrorCode)
	h.Require.Equal(map[string]any{"retry_after_sec": float64(30)}, jsonErr.APIDetails)
}

func TestJSONErrorProxyErrorCarriesStatusOnly(t *testing.T) {
	h := NewCommandHarness(t)
	// A failure reported by beefeater rather than the application: the body
	// has a message under "error" and no code or details.
	h.MockManagementAPI().SetRoute("GET", billingUsagePath, 401, map[string]any{"error": "Unauthorized"})

	h.Require.Error(h.Execute("org", "billing", "usage", "--output", "json"))
	h.Require.Equal(int(cmd.ExitAuth), h.ExitCode)

	jsonErr := decodeJSONErrorEnvelope(h)
	h.Require.Equal("ErrAuth", jsonErr.Type)
	h.Require.Equal(401, jsonErr.APIStatusCode)
	h.Require.Empty(jsonErr.APIErrorCode)
	h.Require.Empty(jsonErr.APIDetails)
}

func TestJSONErrorNonJSONBodyCarriesStatusOnly(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRouteFunc("GET", billingUsagePath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(502)
		_, _ = w.Write([]byte("<html>Bad Gateway</html>"))
	})

	h.Require.Error(h.Execute("org", "billing", "usage", "--output", "json"))

	jsonErr := decodeJSONErrorEnvelope(h)
	h.Require.Equal("ErrServer", jsonErr.Type)
	h.Require.Equal(502, jsonErr.APIStatusCode)
	h.Require.Empty(jsonErr.APIErrorCode)
	h.Require.Empty(jsonErr.APIDetails)
}

func TestJSONErrorLocalFailureHasNoAPIFields(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetHandlerFallback(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatalf("server should not be hit on a flag validation error")
	})

	h.Require.Error(h.Execute("org", "billing", "usage", "--output", "json", "--since", "7d", "--start", "2026-05-01"))
	h.Require.Equal(int(cmd.ExitUsage), h.ExitCode)

	jsonErr := decodeJSONErrorEnvelope(h)
	h.Require.Contains(jsonErr.Message, "--since cannot be combined with")
	h.Require.Equal("ErrUsage", jsonErr.Type)
	h.Require.Zero(jsonErr.APIStatusCode)
	h.Require.Empty(jsonErr.APIErrorCode)
	h.Require.Empty(jsonErr.APIDetails)
}

func TestJSONErrorTextOutputWritesNothingToStdout(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", billingUsagePath, 500, map[string]any{
		"code": "INTERNAL_ERROR", "message": "Internal error",
	})

	h.Require.Error(h.Execute("org", "billing", "usage"))
	h.Require.Empty(h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "Internal error")
}

func TestJSONErrorJSONLEnvelopeIsOneLine(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", billingUsagePath, 500, map[string]any{
		"code": "INTERNAL_ERROR", "message": "Internal error",
	})

	h.Require.Error(h.Execute("org", "billing", "usage", "--output", "jsonl"))
	h.Require.Len(strings.Split(strings.TrimSpace(h.Stdout.String()), "\n"), 1)
	h.Require.Equal("ErrServer", decodeJSONErrorEnvelope(h).Type)
}

func TestJSONErrorBypassesJQ(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", billingUsagePath, 404, map[string]any{
		"code": "NOT_FOUND", "message": "No usage recorded",
	})

	// --jq selects a field of the payload, which never arrived. The envelope
	// is emitted whole rather than run through the expression.
	h.Require.Error(h.Execute("org", "billing", "usage", "--jq", ".dedicated_usage"))
	h.Require.Equal("ErrNotFound", decodeJSONErrorEnvelope(h).Type)
}

func TestJSONErrorUnknownFlagIsAUsageError(t *testing.T) {
	h := NewCommandHarness(t)

	h.Require.Error(h.Execute("org", "billing", "usage", "--output", "json", "--bogus"))
	h.Require.Equal(int(cmd.ExitUsage), h.ExitCode)

	jsonErr := decodeJSONErrorEnvelope(h)
	h.Require.Contains(jsonErr.Message, "unknown flag: --bogus")
	h.Require.Equal("ErrUsage", jsonErr.Type)
	h.Require.Zero(jsonErr.APIStatusCode)
}

func TestJSONErrorUnknownSubcommandIsAUsageError(t *testing.T) {
	h := NewCommandHarness(t)

	h.Require.Error(h.Execute("org", "billing", "bogus", "--output", "json"))
	h.Require.Equal(int(cmd.ExitUsage), h.ExitCode)
	h.Require.Contains(decodeJSONErrorEnvelope(h).Message, "unknown command")
}

func TestJSONErrorBadOutputValueWritesStderrOnly(t *testing.T) {
	h := NewCommandHarness(t)

	// The format itself is what failed to parse, so there is no telling which
	// format the caller wanted the error in.
	h.Require.Error(h.Execute("org", "billing", "usage", "--output", "jsonnn"))
	h.Require.Equal(int(cmd.ExitUsage), h.ExitCode)
	h.Require.Empty(h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "must be one of")
}

func TestJSONErrorRootHelpDocumentsEnvelope(t *testing.T) {
	h := NewCommandHarness(t)
	h.Require.NoError(h.Execute("--help-output"))
	out := h.Stdout.String()
	h.Require.Contains(out, "ErrInterrupted")
	h.Require.Contains(out, "JSON ERRORS")
	h.Require.Contains(out, "api_error_code")
}

// An interrupt is reported as an envelope like any other failure. Uses the
// watch harness because a signal mid-run is the only way to reach the path.
func TestJSONErrorInterruptIsAnEnvelope(t *testing.T) {
	h, m, dir := newModelWatchHarness(t)
	interruptWatchOnSync(h, m)

	h.Require.Error(h.Execute("model", "watch", "--dir", dir, "--output", "json"))
	h.Require.Equal(int(cmd.ExitInterrupted), h.ExitCode)

	jsonErr := decodeJSONErrorEnvelope(h)
	h.Require.Equal("ErrInterrupted", jsonErr.Type)
	h.Require.Equal(cmd.ExitInterrupted, jsonErr.ExitCode)
	h.Require.Zero(jsonErr.APIStatusCode)
}
