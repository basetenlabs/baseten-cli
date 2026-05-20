package cmd

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/coder/websocket"
)

func init() {
	Register("model predict", commandModelPredict)
}

// predictResponse is the transport-agnostic result of a predict round trip.
// HTTP gives chunked + content-type from headers; WebSocket synthesizes a
// content-type from the frame type (text -> application/json, bytes ->
// application/octet-stream) and always reports streaming=false.
type predictResponse struct {
	body        io.ReadCloser
	contentType string
	streaming   bool
}

func commandModelPredict(ctx *CommandContext, flags *cmd.ModelPredictFlags) error {
	targets := 0
	if flags.Environment != "" {
		targets++
	}
	if flags.DeploymentID != "" {
		targets++
	}
	if flags.Regional != "" {
		targets++
	}
	if targets > 1 {
		return &ErrUsage{Err: errors.New("--environment, --deployment-id, and --regional are mutually exclusive")}
	}

	mgmtCl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := ResolveModelRef(ctx, mgmtCl.API(), flags.ModelRefFlags)
	if err != nil {
		return err
	}

	icFlags := cmd.InferenceClientFlags{ModelID: ref.ID}
	if flags.Regional != "" {
		icFlags.Environment = flags.Regional
	}
	ic, err := ctx.NewInferenceClient(icFlags)
	if err != nil {
		return err
	}
	api := ic.API()

	body, err := openPredictBody(ctx, flags)
	if err != nil {
		return err
	}
	defer body.Close()

	var resp *predictResponse
	if flags.Websocket {
		resp, err = doWebsocketPredict(ctx, api.HTTPClient, api.BaseURL, flags, body)
	} else {
		resp, err = doHTTPPredict(ctx, api.HTTPClient, api.BaseURL, flags, body)
	}
	if err != nil {
		return err
	}
	defer resp.body.Close()
	return writePredictResponse(ctx, resp)
}

func predictPath(flags *cmd.ModelPredictFlags, scheme string) string {
	suffix := "/predict"
	if scheme == "ws" {
		suffix = "/websocket"
	}
	switch {
	case flags.DeploymentID != "":
		return "/deployment/" + flags.DeploymentID + suffix
	case flags.Regional != "":
		return suffix
	default:
		env := flags.Environment
		if env == "" {
			env = "production"
		}
		return "/environments/" + env + suffix
	}
}

func doHTTPPredict(
	ctx *CommandContext,
	httpClient interface {
		Do(*http.Request) (*http.Response, error)
	},
	baseURL string,
	flags *cmd.ModelPredictFlags,
	body io.Reader,
) (*predictResponse, error) {
	url := strings.TrimRight(baseURL, "/") + predictPath(flags, "http")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	contentType := strings.TrimSpace(strings.SplitN(resp.Header.Get("Content-Type"), ";", 2)[0])
	streaming := false
	for _, te := range resp.TransferEncoding {
		if te == "chunked" {
			streaming = true
		}
	}
	// Wrap so we can surface a non-2xx status code after the body has been
	// written by the formatter.
	rc := &statusAwareBody{ReadCloser: resp.Body, status: resp.StatusCode}
	return &predictResponse{body: rc, contentType: contentType, streaming: streaming}, nil
}

// statusAwareBody wraps an HTTP response body so writePredictResponse can
// return a non-2xx error after writing the body bytes. Without this we'd
// either lose the body output or lose the status code.
type statusAwareBody struct {
	io.ReadCloser
	status int
}

// roundTripperFunc adapts a Do-style function to http.RoundTripper.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func doWebsocketPredict(
	ctx *CommandContext,
	httpClient interface {
		Do(*http.Request) (*http.Response, error)
	},
	baseURL string,
	flags *cmd.ModelPredictFlags,
	body io.Reader,
) (*predictResponse, error) {
	wsURL := wsURLFromBase(baseURL) + predictPath(flags, "ws")

	client := &http.Client{Transport: roundTripperFunc(httpClient.Do)}
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		return nil, fmt.Errorf("websocket dial: %w", err)
	}
	// Default read limit is 32 KiB; raise to predict-sized payloads.
	conn.SetReadLimit(100 * 1024 * 1024)

	payload, err := io.ReadAll(body)
	if err != nil {
		conn.Close(websocket.StatusInternalError, "")
		return nil, fmt.Errorf("reading request body: %w", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
		conn.Close(websocket.StatusInternalError, "")
		return nil, fmt.Errorf("websocket send: %w", err)
	}

	msgType, msg, err := conn.Read(ctx)
	if err != nil {
		// Surface the close code if the server closed before sending a frame.
		conn.Close(websocket.StatusInternalError, "")
		return nil, fmt.Errorf("websocket receive: %w", err)
	}
	contentType := "application/octet-stream"
	if msgType == websocket.MessageText {
		contentType = "application/json"
	}

	// Close cleanly; ignore errors (we already have the frame we needed).
	_ = conn.Close(websocket.StatusNormalClosure, "")

	return &predictResponse{
		body:        io.NopCloser(bytes.NewReader(msg)),
		contentType: contentType,
		streaming:   false,
	}, nil
}

// wsURLFromBase converts an http/https base URL to ws/wss. Inputs without
// an http(s) scheme are returned unchanged (lets tests pass a raw wss URL).
func wsURLFromBase(base string) string {
	trimmed := strings.TrimRight(base, "/")
	switch {
	case strings.HasPrefix(trimmed, "https://"):
		return "wss://" + strings.TrimPrefix(trimmed, "https://")
	case strings.HasPrefix(trimmed, "http://"):
		return "ws://" + strings.TrimPrefix(trimmed, "http://")
	}
	return trimmed
}

func openPredictBody(ctx *CommandContext, flags *cmd.ModelPredictFlags) (io.ReadCloser, error) {
	if flags.Data != "" {
		return io.NopCloser(strings.NewReader(flags.Data)), nil
	}
	if flags.File == "-" {
		return io.NopCloser(ctx.Stdin), nil
	}
	f, err := os.Open(flags.File)
	if err != nil {
		return nil, fmt.Errorf("opening input file: %w", err)
	}
	return f, nil
}

func writePredictResponse(ctx *CommandContext, resp *predictResponse) error {
	if resp.streaming {
		if err := writePredictStreamingResponse(ctx, resp.body, resp.contentType == "text/event-stream"); err != nil {
			return err
		}
	} else if resp.contentType == "application/json" {
		if err := writePredictJSONResponse(ctx, resp.body); err != nil {
			return err
		}
	} else {
		if err := writePredictBinaryResponse(ctx, resp.body); err != nil {
			return err
		}
	}
	if sa, ok := resp.body.(*statusAwareBody); ok && sa.status >= 400 {
		return fmt.Errorf("request failed with status %d", sa.status)
	}
	return nil
}

func writePredictJSONResponse(ctx *CommandContext, body io.Reader) error {
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}
	if len(bodyBytes) == 0 {
		return nil
	}
	var parsed any
	if err := json.Unmarshal(bodyBytes, &parsed); err != nil {
		ctx.Output(string(bodyBytes))
		return nil
	}
	ctx.OutputJSON(parsed)
	return nil
}

func writePredictBinaryResponse(ctx *CommandContext, body io.Reader) error {
	if !ctx.JSON {
		if _, err := io.Copy(ctx.Stdout, body); err != nil {
			return fmt.Errorf("writing response: %w", err)
		}
		return nil
	}
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}
	ctx.OutputJSON(map[string]any{
		"body": base64.StdEncoding.EncodeToString(bodyBytes),
	})
	return nil
}

func writePredictStreamingResponse(ctx *CommandContext, body io.Reader, isSSE bool) error {
	if !ctx.JSON {
		if _, err := io.Copy(ctx.Stdout, body); err != nil {
			return fmt.Errorf("streaming response: %w", err)
		}
		return nil
	}
	if !ctx.JSONLines {
		ctx.LogLine("warning: streaming response cannot be emitted as a single JSON document; output will be JSONL")
		// Force compact, line-delimited records for the rest of this command.
		ctx.JSONCompact = true
	}
	if isSSE {
		return streamPredictSSEAsJSONL(ctx, body)
	}
	return streamPredictBinaryAsJSONL(ctx, body)
}

func streamPredictSSEAsJSONL(ctx *CommandContext, body io.Reader) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(line[len("data:"):])
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var parsed any
		if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
			ctx.OutputJSON(map[string]any{
				"body": base64.StdEncoding.EncodeToString([]byte(payload)),
			})
			continue
		}
		ctx.OutputJSON(parsed)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading SSE stream: %w", err)
	}
	return nil
}

func streamPredictBinaryAsJSONL(ctx *CommandContext, body io.Reader) error {
	buf := make([]byte, 4096)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			ctx.OutputJSON(map[string]any{
				"body": base64.StdEncoding.EncodeToString(buf[:n]),
			})
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("streaming response: %w", err)
		}
	}
}
