package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("model-api describe", commandModelAPIDescribe)
	Register("model-api list", commandModelAPIList)
	Register("model-api predict", commandModelAPIPredict)
}

func commandModelAPIList(ctx *CommandContext, flags *cmd.ModelAPIListFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}

	// The catalog is small, so walk every page and aggregate into one list
	// rather than exposing cursors. By default restrict to the Model APIs the
	// workspace has added; --all browses the full visible catalog.
	params := managementapi.GetV1ModelApisParams{}
	if !flags.All {
		addedOnly := true
		params.AddedOnly = &addedOnly
	}
	var items []managementapi.ModelAPI
	for {
		resp, err := cl.API().GetModelApis(ctx, params)
		if err != nil {
			return fmt.Errorf("list model APIs: %w", err)
		}
		items = append(items, resp.Items...)
		if !resp.Pagination.HasMore || resp.Pagination.Cursor == nil {
			break
		}
		params.Cursor = resp.Pagination.Cursor
	}

	if ctx.JSON {
		ctx.OutputJSON(cmd.ModelAPIList{Items: items})
		return nil
	}
	if len(items) == 0 {
		ctx.LogLine("No Model APIs found.")
		return nil
	}
	rows := make([][]string, 0, len(items))
	for _, m := range items {
		added := ""
		if m.OrgDetails != nil {
			added = "yes"
		}
		rows = append(rows, []string{
			m.Name, fmt.Sprintf("%d", m.ContextLength),
			modelAPICurrencyString(m.CostPerMillionInputTokens),
			modelAPICurrencyString(m.CostPerMillionOutputTokens),
			added,
		})
	}
	ctx.OutputTable(TableOutput{
		Headers:             []string{"NAME", "CONTEXT", "$/1M IN", "$/1M OUT", "ADDED"},
		Rows:                rows,
		RightAlignedColumns: []int{1, 2, 3},
	})
	return nil
}

func commandModelAPIDescribe(ctx *CommandContext, flags *cmd.ModelAPIDescribeFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	m, err := cl.API().GetModelApisModelApiName(ctx, flags.Model)
	if err != nil {
		return fmt.Errorf("fetch model API %s: %w", flags.Model, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(m)
		return nil
	}
	ctx.Outputf("Name:            %s\n", m.Name)
	ctx.Outputf("Display Name:    %s\n", m.DisplayName)
	if m.ModelFamily != nil {
		ctx.Outputf("Family:          %s\n", *m.ModelFamily)
	}
	if m.Description != "" {
		ctx.Outputf("Description:     %s\n", m.Description)
	}
	ctx.Outputf("Release Date:    %s\n", m.ReleaseDate)
	ctx.Outputf("Context Length:  %d\n", m.ContextLength)
	ctx.Outputf("Input Cost:      $%s / 1M tokens\n", modelAPICurrencyString(m.CostPerMillionInputTokens))
	ctx.Outputf("Output Cost:     $%s / 1M tokens\n", modelAPICurrencyString(m.CostPerMillionOutputTokens))
	ctx.Outputf("Invoke URL:      %s\n", m.InvokeUrl)
	if len(m.RateLimits) > 0 {
		ctx.Outputf("Rate Limits:     %s\n", modelAPIRateLimitsString(m.RateLimits))
	}
	if m.OrgDetails != nil {
		ctx.Outputf("Added:           %s\n", m.OrgDetails.AddedAt.UTC().Format(time.RFC3339))
		if m.OrgDetails.LastUsedAt != nil {
			ctx.Outputf("Last Used:       %s\n", m.OrgDetails.LastUsedAt.UTC().Format(time.RFC3339))
		}
	}
	return nil
}

func commandModelAPIPredict(ctx *CommandContext, flags *cmd.ModelAPIPredictFlags) error {
	payload, err := modelAPIPredictBody(ctx, flags)
	if err != nil {
		return err
	}

	// Build a transport that injects the active credential, then POST straight
	// to the target URL. No management lookup is needed: the model is selected
	// by the request body, not the URL path.
	transport, remote, err := ctx.AuthTransport()
	if err != nil {
		return err
	}

	// Default to the remote's chat-completions endpoint when --url is unset.
	url := flags.URL
	if url == "" {
		url = remote.ModelAPIInferenceURL() + "/v1/chat/completions"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := transport.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// --content opts into chat-aware text output: print just the assistant's
	// reply. Under --output json the full response is emitted via the verbatim
	// path below.
	if flags.Content != "" && !ctx.JSON {
		return writeModelAPIChatText(ctx, resp)
	}

	// Verbatim path: hand the response to the shared predict writer, which
	// pretty-prints JSON and passes streams and binary bodies through.
	contentType := strings.TrimSpace(strings.SplitN(resp.Header.Get("Content-Type"), ";", 2)[0])
	streaming := false
	for _, te := range resp.TransferEncoding {
		if te == "chunked" {
			streaming = true
		}
	}
	return writePredictResponse(ctx, &predictResponse{
		body:        &statusAwareBody{ReadCloser: resp.Body, status: resp.StatusCode},
		contentType: contentType,
		streaming:   streaming,
	})
}

// modelAPIPredictBody resolves the request body from exactly one of --content,
// --data, or --file. --content builds a single-message OpenAI chat-completions
// request with --model as the model; --data and --file are verbatim.
func modelAPIPredictBody(ctx *CommandContext, flags *cmd.ModelAPIPredictFlags) ([]byte, error) {
	switch {
	case flags.Content != "":
		if flags.Model == "" {
			return nil, cmd.NewErrUsagef("--content requires --model")
		}
		return json.Marshal(map[string]any{
			"model": flags.Model,
			"messages": []map[string]string{
				{"role": "user", "content": flags.Content},
			},
		})
	case flags.Data != "":
		return []byte(flags.Data), nil
	case flags.File == "-":
		body, err := io.ReadAll(ctx.Stdin)
		if err != nil {
			return nil, fmt.Errorf("reading stdin: %w", err)
		}
		return body, nil
	case flags.File != "":
		body, err := os.ReadFile(flags.File)
		if err != nil {
			return nil, fmt.Errorf("opening input file: %w", err)
		}
		return body, nil
	default:
		return nil, cmd.NewErrUsagef("one of --content, --data, or --file is required")
	}
}

// writeModelAPIChatText prints the assistant message from an OpenAI
// chat-completions response. Non-2xx responses and bodies that don't match the
// expected shape are surfaced as-is.
func writeModelAPIChatText(ctx *CommandContext, resp *http.Response) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}
	if resp.StatusCode >= 400 {
		ctx.Log(string(body))
		return fmt.Errorf("request failed with status %d", resp.StatusCode)
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || len(parsed.Choices) == 0 {
		// Not the expected chat shape; pass the body through unchanged.
		ctx.Output(string(body))
		return nil
	}
	ctx.OutputLine(parsed.Choices[0].Message.Content)
	return nil
}

// modelAPICurrencyString renders a cost union (number or string) as a plain
// decimal string, dropping the JSON quoting when the value arrives as a string.
func modelAPICurrencyString(m json.Marshaler) string {
	b, err := m.MarshalJSON()
	if err != nil {
		return ""
	}
	return strings.Trim(string(b), `"`)
}

func modelAPIRateLimitsString(limits []managementapi.RateLimit) string {
	parts := make([]string, 0, len(limits))
	for _, l := range limits {
		parts = append(parts, fmt.Sprintf("%d per %s (%s)", l.Threshold, l.Unit, l.Type))
	}
	return strings.Join(parts, ", ")
}
