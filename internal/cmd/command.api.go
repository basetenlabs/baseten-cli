package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/basetenlabs/baseten-cli/cmd"
)

func init() {
	Register("api management", commandAPIManagement)
	Register("api inference", commandAPIInference)
}

func commandAPIManagement(ctx *CommandContext, flags *cmd.APIManagementFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	api := cl.API()
	// Accept paths with or without a leading /v1 since the docs show the v1 prefix.
	ctx.Args[0] = strings.TrimPrefix(strings.TrimPrefix(ctx.Args[0], "/"), "v1/")
	return callAPI(ctx, &flags.APIFlags, strings.TrimRight(api.BaseURL, "/")+"/v1", api.HTTPClient, api.Headers)
}

func commandAPIInference(ctx *CommandContext, flags *cmd.APIInferenceFlags) error {
	cl, err := ctx.NewInferenceClient(flags.InferenceClientFlags)
	if err != nil {
		return cmd.NewErrUsage(err)
	}
	api := cl.API()
	return callAPI(ctx, &flags.APIFlags, api.BaseURL, api.HTTPClient, api.Headers)
}

func callAPI(ctx *CommandContext, flags *cmd.APIFlags, baseURL string, httpClient interface {
	Do(*http.Request) (*http.Response, error)
}, headers http.Header) error {
	req, cleanup, err := buildAPIRequest(ctx, flags, baseURL)
	defer cleanup()
	if err != nil {
		return err
	}

	// Apply client headers (e.g. auth) without overriding request-specific ones.
	for key, vals := range headers {
		if req.Header.Get(key) == "" {
			for _, val := range vals {
				req.Header.Add(key, val)
			}
		}
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if err := outputAPIResponse(ctx, resp); err != nil {
		return err
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf("request failed with status %d", resp.StatusCode)
	}
	return nil
}

// buildAPIRequest builds an HTTP request. The returned cleanup function is
// never nil and must always be called.
func buildAPIRequest(ctx *CommandContext, flags *cmd.APIFlags, baseURL string) (*http.Request, func(), error) {
	cleanup := func() {}

	apiPath := ctx.Args[0]
	hasFields := len(flags.Field) > 0 || len(flags.RawField) > 0
	if hasFields && flags.Input != "" {
		return nil, cleanup, cmd.NewErrUsagef("--input is mutually exclusive with --field and --raw-field")
	}

	var body io.Reader
	hasBody := hasFields || flags.Input != ""
	if hasFields {
		bodyMap := map[string]any{}
		for _, f := range flags.RawField {
			k, v, ok := strings.Cut(f, "=")
			if !ok {
				return nil, cleanup, fmt.Errorf("invalid raw-field format %q, expected key=value", f)
			}
			bodyMap[k] = v
		}
		for _, f := range flags.Field {
			k, v, ok := strings.Cut(f, "=")
			if !ok {
				return nil, cleanup, fmt.Errorf("invalid field format %q, expected key=value", f)
			}
			var parsed any
			if err := json.Unmarshal([]byte(v), &parsed); err != nil {
				return nil, cleanup, fmt.Errorf("invalid JSON value for field %q: %w", k, err)
			}
			bodyMap[k] = parsed
		}
		bodyBytes, err := json.Marshal(bodyMap)
		if err != nil {
			return nil, cleanup, fmt.Errorf("marshaling request body: %w", err)
		}
		body = bytes.NewReader(bodyBytes)
	} else if flags.Input != "" {
		if flags.Input == "-" {
			body = os.Stdin
		} else {
			f, err := os.Open(flags.Input)
			if err != nil {
				return nil, cleanup, fmt.Errorf("opening input file: %w", err)
			}
			cleanup = func() { f.Close() }
			body = f
		}
	}

	method := flags.Method
	if method == "" {
		if hasBody {
			method = http.MethodPost
		} else {
			method = http.MethodGet
		}
	}

	url := strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(apiPath, "/")

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, cleanup, fmt.Errorf("creating request: %w", err)
	}
	if hasBody {
		req.Header.Set("Content-Type", "application/json")
	}

	for _, h := range flags.Header {
		k, v, ok := strings.Cut(h, ":")
		if !ok {
			return nil, cleanup, fmt.Errorf("invalid header format %q, expected key:value", h)
		}
		req.Header.Set(strings.TrimSpace(k), strings.TrimSpace(v))
	}

	return req, cleanup, nil
}

func outputAPIResponse(ctx *CommandContext, resp *http.Response) error {
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("reading response: %w", err)
		}
		if len(respBody) > 0 {
			var parsed any
			if err := json.Unmarshal(respBody, &parsed); err == nil {
				ctx.OutputJSON(parsed)
			} else {
				ctx.Output(string(respBody))
			}
		}
	} else if _, err := io.Copy(ctx.Stdout, resp.Body); err != nil {
		return fmt.Errorf("reading response: %w", err)
	}
	return nil
}
