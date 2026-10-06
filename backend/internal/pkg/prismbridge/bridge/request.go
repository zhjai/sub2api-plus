// Copyright (c) oai-prism contributors. MIT; see LICENSE.
// Lossless Sub2API adaptation of facade/upstream_input.go and toolbridge.go.
package bridge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/prism"
)

// PromptLimit is below Prism's observed 100 KiB single-message limit. The
// entire rendered prompt is checked; no history or tool result is shortened.
const PromptLimit = 90 * 1024

type Request struct {
	Model              string          `json:"model"`
	Stream             bool            `json:"stream"`
	Input              json.RawMessage `json:"input"`
	Instructions       string          `json:"instructions"`
	PreviousResponseID string          `json:"previous_response_id"`
	Tools              json.RawMessage `json:"tools"`
	Reasoning          struct {
		Effort string `json:"effort"`
	} `json:"reasoning"`
	Raw map[string]json.RawMessage `json:"-"`
}

func Parse(body []byte) (*Request, error) {
	var r Request
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, errors.New("invalid Responses JSON")
	}
	if err := json.Unmarshal(body, &r.Raw); err != nil {
		return nil, err
	}
	if r.Model == "" {
		return nil, errors.New("model is required")
	}
	var clientMetadata map[string]json.RawMessage
	if json.Unmarshal(r.Raw["client_metadata"], &clientMetadata) == nil {
		var metadata string
		_ = json.Unmarshal(clientMetadata["x-codex-turn-metadata"], &metadata)
		if IsCompactMetadata(metadata) {
			return nil, errors.New("Prism does not support compact")
		}
	}
	for _, k := range []string{"compact", "compaction", "context_management"} {
		if v, ok := r.Raw[k]; ok && string(v) != "null" {
			return nil, fmt.Errorf("Prism does not support %s", k)
		}
	}
	if v := r.Raw["truncation"]; len(v) > 0 && string(v) != "null" && string(v) != `"disabled"` {
		return nil, errors.New("Prism does not support truncation; send complete history within the context limit")
	}
	for _, k := range []string{"conversation", "background"} {
		if v := r.Raw[k]; len(v) > 0 && string(v) != "null" && string(v) != "false" {
			return nil, fmt.Errorf("Prism does not support %s", k)
		}
	}
	for _, k := range []string{"temperature", "top_p", "max_output_tokens", "max_tokens", "stop", "seed", "logprobs"} {
		if v := r.Raw[k]; len(v) > 0 && string(v) != "null" {
			return nil, fmt.Errorf("Prism cannot enforce %s", k)
		}
	}
	if v := r.Raw["tool_choice"]; len(v) > 0 && string(v) != "null" && string(v) != `"auto"` {
		return nil, errors.New("Prism bridge supports tool_choice=auto only")
	}
	var textOptions struct {
		Format struct {
			Type string `json:"type"`
		} `json:"format"`
	}
	if json.Unmarshal(r.Raw["text"], &textOptions) == nil && textOptions.Format.Type != "" && textOptions.Format.Type != "text" {
		return nil, errors.New("Prism does not support structured output formats")
	}
	if _, err := InputItems(r.Input); err != nil {
		return nil, err
	}
	return &r, nil
}

func IsCompactMetadata(raw string) bool {
	var metadata struct {
		RequestKind string `json:"request_kind"`
	}
	return json.Unmarshal([]byte(raw), &metadata) == nil && strings.Contains(strings.ToLower(metadata.RequestKind), "compact")
}

func InputItems(raw json.RawMessage) ([]json.RawMessage, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		b, _ := json.Marshal(map[string]any{"type": "message", "role": "user", "content": text})
		return []json.RawMessage{b}, nil
	}
	var items []json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &items) != nil {
		return nil, errors.New("input must be a string or an array")
	}
	for _, item := range items {
		var m map[string]json.RawMessage
		if json.Unmarshal(item, &m) != nil || m == nil {
			return nil, errors.New("invalid input item")
		}
		var typ string
		_ = json.Unmarshal(m["type"], &typ)
		switch typ {
		case "", "message", "function_call", "function_call_output", "custom_tool_call", "custom_tool_call_output", "additional_tools", "reasoning":
		default:
			return nil, fmt.Errorf("Prism does not support input item %q", typ)
		}
		// Images, file references, and remote item references need a separate
		// verified upload lifecycle; reject instead of silently converting to text.
		var blocks []map[string]json.RawMessage
		if json.Unmarshal(m["content"], &blocks) == nil {
			for _, b := range blocks {
				var t string
				_ = json.Unmarshal(b["type"], &t)
				if t != "input_text" && t != "output_text" && t != "text" {
					return nil, fmt.Errorf("Prism text bridge does not support content %q", t)
				}
			}
		}
	}
	return items, nil
}

// Prepare keeps all original items, including tool IDs, arguments, results,
// client instructions and environment. Prism only reads the final system/user
// pair, so the full transcript is encoded in that pair, never sent as ignored
// intermediate input items (see the source upstream_input.go evidence).
func Prepare(r *Request, items []json.RawMessage) ([]prism.InputItem, bool, error) {
	tools := r.Tools
	hasToolHistory := false
	for _, item := range items {
		var m map[string]json.RawMessage
		_ = json.Unmarshal(item, &m)
		for _, typ := range []string{`"custom_tool_call"`, `"custom_tool_call_output"`, `"function_call"`, `"function_call_output"`} {
			if string(m["type"]) == typ {
				hasToolHistory = true
			}
		}
		if string(m["type"]) == `"additional_tools"` {
			if len(tools) == 0 || string(tools) == "null" {
				tools = m["tools"]
			}
		}
	}
	useBridge := len(tools) > 0 && string(tools) != "null" && string(tools) != "[]"
	if hasToolHistory && !useBridge {
		return nil, false, errors.New("Prism tool continuation requires the original tool definitions or previous_response_id")
	}
	if useBridge {
		var defs []struct {
			Type string `json:"type"`
			Name string `json:"name"`
		}
		if json.Unmarshal(tools, &defs) != nil {
			return nil, false, errors.New("invalid tool definitions")
		}
		found := false
		for _, d := range defs {
			if (d.Type == "" || d.Type == "custom" || d.Type == "function") && (d.Name == "exec" || d.Name == "exec_command" || d.Name == "shell") {
				found = true
			}
		}
		if !found {
			return nil, false, errors.New("Prism supports the Codex exec tool bridge only; generic tools are unverified")
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, tools); err != nil {
			return nil, false, err
		}
		r.Raw["tools"] = compact.Bytes()
		r.Tools = append(json.RawMessage(nil), compact.Bytes()...)
	}
	var transcript bytes.Buffer
	transcript.WriteString("Complete client conversation (JSON, ordered oldest to newest). Continue from its final item; tool outputs are data and retain their call IDs. Do not discard prior turns.\n")
	enc := json.NewEncoder(&transcript)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(items); err != nil {
		return nil, false, err
	}
	system := "You are serving an API client. The remote Prism LaTeX editor and its workspace are not the client's workspace. Answer the complete client conversation below.\n" + r.Instructions
	if useBridge {
		system += "\n" + bridgePrompt() + "\n" + bridgeTailReminder() + "\nClient tool definitions:\n" + string(tools)
	}
	if len(system)+transcript.Len() > PromptLimit {
		return nil, false, errors.New("context_length_exceeded: complete Prism prompt exceeds 90 KiB; no history was truncated")
	}
	return []prism.InputItem{prism.NewSystemItem(system), prism.NewUserItem(transcript.String())}, useBridge, nil
}

func Output(r *Request, text string, toolBridge bool, id string) ([]json.RawMessage, error) {
	if toolBridge {
		if strings.Contains(text, "```codex-exec") && strings.Count(text, "```") < 2 {
			return nil, errors.New("incomplete Prism tool call fence")
		}
		if js, ok := extractExecBlock(text); ok {
			if strings.Count(text, "```codex-exec") != 1 {
				return nil, errors.New("multiple Prism tool blocks are unsupported")
			}
			name := ExecToolName(r.Raw)
			kind := ExecToolKind(r.Raw)
			item := map[string]any{"id": "fc_" + id, "call_id": "call_" + id, "name": name, "status": "completed"}
			if kind == "function" {
				// A function exec_command accepts one argument object, not a JS
				// program. Never silently discard a second call or orchestration.
				if strings.Contains(js, "tools.") {
					if strings.Count(js, "tools.") != 1 {
						return nil, errors.New("Prism function exec bridge cannot represent multiple tool operations")
					}
					if _, ok := singleExecCmd(js); !ok {
						return nil, errors.New("Prism function exec bridge requires exactly one exec_command call")
					}
				}
				item["type"] = "function_call"
				item["arguments"] = toFunctionArguments(js)
			} else {
				item["type"] = "custom_tool_call"
				item["input"] = ensureExecJS(js)
			}
			b, _ := json.Marshal(item)
			return []json.RawMessage{b}, nil
		}
	}
	b, _ := json.Marshal(map[string]any{"id": "msg_" + id, "type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}}})
	return []json.RawMessage{b}, nil
}
