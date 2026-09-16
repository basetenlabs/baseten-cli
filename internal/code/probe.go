package code

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const probeTool = "baseten_code_probe"

// Probe performs two streamed turns with a synthetic, side-effect-free tool.
// No model-generated command is executed. The marker is generated after the
// tool call, so merely repeating the initial prompt cannot pass replay.
func (c *Client) Probe(ctx context.Context, harness, model string) error {
	canonical, err := Canonical(harness)
	if err != nil {
		return err
	}
	if canonical != "codex" && canonical != "claude-code" {
		return fmt.Errorf("protocol probes for %s are not implemented", harness)
	}
	schema := map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
	prompt := "Call baseten_code_probe with no arguments. Then reply with exactly the marker returned by that tool."
	var path string
	var first map[string]any
	if canonical == "codex" {
		path = "/v1/responses"
		first = map[string]any{"model": model, "stream": true, "max_output_tokens": 512, "input": prompt,
			"tools":       []any{map[string]any{"type": "function", "name": probeTool, "description": "Return a diagnostic marker", "parameters": schema}},
			"tool_choice": map[string]any{"type": "function", "name": probeTool},
		}
	} else {
		path = "/v1/messages"
		first = map[string]any{"model": model, "stream": true, "max_tokens": 512, "messages": []any{map[string]any{"role": "user", "content": prompt}},
			"tools":       []any{map[string]any{"name": probeTool, "description": "Return a diagnostic marker", "input_schema": schema}},
			"tool_choice": map[string]any{"type": "tool", "name": probeTool},
		}
	}
	raw, err := c.request(ctx, "POST", path, first, true)
	if err != nil {
		return err
	}
	blocks, _, err := parseProbeStream(canonical, raw)
	if err != nil {
		return err
	}
	var tool map[string]any
	for _, block := range blocks {
		kind, _ := block["type"].(string)
		if kind == "function_call" || kind == "tool_use" {
			if tool != nil {
				return errors.New("probe returned more than one tool call")
			}
			tool = block
		}
	}
	if tool == nil || tool["name"] != probeTool {
		return errors.New("stream did not contain the requested diagnostic tool call")
	}
	entropy := make([]byte, 12)
	if _, err := rand.Read(entropy); err != nil {
		return err
	}
	marker := "BASETEN_CODE_" + hex.EncodeToString(entropy)
	second := map[string]any{"model": model, "stream": true}
	if canonical == "codex" {
		id, _ := tool["call_id"].(string)
		if id == "" {
			return errors.New("Responses tool call is missing call_id")
		}
		second["max_output_tokens"] = 512
		input := []any{map[string]any{"role": "user", "content": prompt}}
		for _, block := range blocks {
			input = append(input, block)
		}
		input = append(input, map[string]any{"type": "function_call_output", "call_id": id, "output": marker})
		second["input"] = input
	} else {
		id, _ := tool["id"].(string)
		if id == "" {
			return errors.New("Messages tool call is missing id")
		}
		second["max_tokens"] = 512
		second["messages"] = []any{map[string]any{"role": "user", "content": prompt}, map[string]any{"role": "assistant", "content": blocks}, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": marker}}}}
	}
	raw, err = c.request(ctx, "POST", path, second, true)
	if err != nil {
		return err
	}
	_, text, err := parseProbeStream(canonical, raw)
	if err != nil {
		return err
	}
	if !strings.Contains(text, marker) {
		return errors.New("streamed response did not replay the diagnostic tool result")
	}
	return nil
}

// SSE permits multiple data lines per event and comments between events.
func streamEvents(raw []byte) ([]map[string]any, error) {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), 8<<20)
	events := []map[string]any{}
	data := []string{}
	flush := func() error {
		if len(data) == 0 {
			return nil
		}
		payload := strings.Join(data, "\n")
		data = nil
		if payload == "[DONE]" {
			return nil
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(payload), &event); err != nil || event == nil {
			return errors.New("invalid JSON in gateway event stream")
		}
		events = append(events, event)
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return nil, err
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, errors.New("invalid gateway event stream")
	}
	if len(data) > 0 {
		return nil, errors.New("truncated gateway event stream")
	}
	return events, nil
}
func parseProbeStream(harness string, raw []byte) ([]map[string]any, string, error) {
	events, err := streamEvents(raw)
	if err != nil {
		return nil, "", err
	}
	blocks := []map[string]any{}
	text := ""
	completed := false
	indices := map[int]map[string]any{}
	partial := map[int]string{}
	for _, event := range events {
		kind, _ := event["type"].(string)
		if kind == "error" || kind == "response.failed" || kind == "response.incomplete" {
			return nil, "", errors.New("gateway stream reported a failed or incomplete response")
		}
		if harness == "codex" && kind == "response.completed" {
			response, ok := event["response"].(map[string]any)
			if !ok || response["status"] != "completed" {
				return nil, "", errors.New("invalid Responses completion event")
			}
			output, ok := response["output"].([]any)
			if !ok {
				return nil, "", errors.New("Responses completion is missing output")
			}
			for _, item := range output {
				block, ok := item.(map[string]any)
				if !ok {
					return nil, "", errors.New("invalid Responses output item")
				}
				blocks = append(blocks, block)
				if content, ok := block["content"].([]any); ok {
					for _, part := range content {
						if p, ok := part.(map[string]any); ok && p["type"] == "output_text" {
							if t, ok := p["text"].(string); ok {
								text += t
							}
						}
					}
				}
			}
			completed = true
		}
		if harness == "claude-code" {
			index, _ := event["index"].(float64)
			n := int(index)
			switch kind {
			case "content_block_start":
				block, ok := event["content_block"].(map[string]any)
				if !ok {
					return nil, "", errors.New("invalid Messages content block")
				}
				indices[n] = block
				blocks = append(blocks, block)
			case "content_block_delta":
				delta, ok := event["delta"].(map[string]any)
				if !ok {
					return nil, "", errors.New("invalid Messages delta")
				}
				if delta["type"] == "text_delta" {
					t, _ := delta["text"].(string)
					text += t
					if block := indices[n]; block != nil {
						old, _ := block["text"].(string)
						block["text"] = old + t
					}
				}
				if delta["type"] == "input_json_delta" {
					s, _ := delta["partial_json"].(string)
					partial[n] += s
				}
			case "message_delta":
				delta, _ := event["delta"].(map[string]any)
				if delta["stop_reason"] == "max_tokens" {
					return nil, "", errors.New("Messages stream exceeded probe token limit")
				}
			case "message_stop":
				completed = true
			}
		}
	}
	for index, raw := range partial {
		block := indices[index]
		if block == nil {
			return nil, "", errors.New("tool input has no content block")
		}
		var input any
		if json.Unmarshal([]byte(raw), &input) != nil {
			return nil, "", errors.New("invalid streamed tool input")
		}
		block["input"] = input
	}
	if !completed {
		return nil, "", errors.New("gateway stream ended without a completion event")
	}
	return blocks, text, nil
}
