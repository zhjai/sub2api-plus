package service

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

const openAIEvalCodexRequestProfile = "codex-turn-cpa-876d1ab4-v1"

//go:embed data/codex_eval/codex_context.json
var openAIEvalCodexContextJSON []byte

//go:embed data/codex_eval/LICENSE
var openAIEvalCodexContextLicense string

type openAIEvalCodexText struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// Each sample decodes its own mutable context: transforms must not mutate the
// embedded fixture or another concurrent evaluation's tool declarations.
func newOpenAIEvalCodexPayload(credential *Account, model, effort, prompt string, now time.Time) (map[string]any, string, error) {
	var captured struct {
		Tools            []any  `json:"tools"`
		BaseInstructions string `json:"base_instructions"`
		Messages         []struct {
			Role    string                `json:"role"`
			Content []openAIEvalCodexText `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(openAIEvalCodexContextJSON, &captured); err != nil {
		return nil, "", fmt.Errorf("decode Codex evaluation context: %w", err)
	}
	thread, err := uuid.NewV7()
	if err != nil {
		return nil, "", fmt.Errorf("create independent Codex evaluation thread: %w", err)
	}
	session, turn, window := thread.String(), uuid.NewString(), thread.String()+":0"
	namespace := codexAccountIdentityNamespace(credential)
	installation := uuid.NewString()
	if namespace != "" {
		installation = deriveStableUUIDv4("sub2api:codex-evaluation-installation:" + namespace)
	}
	metadata := map[string]any{
		"installation_id": installation, "session_id": session, "thread_id": session,
		"agent_name": "/root", "turn_id": turn, "window_id": window,
		"window_number": 0, "context_window_id": uuid.NewString(), "request_kind": "turn",
		"root_turn_id": turn, "thread_source": "user", "turn_trigger": "user",
		"sandbox": "seccomp", "sandbox_mode": "workspace-write", "auto_review_enabled": true,
		"node_repl_auto_review_required": true, "node_repl_disabled": false,
		"turn_started_at_unix_ms": now.UnixMilli(), "analytics_enabled": true, "model": model,
	}
	effort = strings.ToLower(strings.TrimSpace(effort))
	reasoning := map[string]any{"context": "all_turns"}
	if effort != "" {
		reasoning["effort"] = effort
		metadata["reasoning_effort"] = effort
	}
	turnMetadata, err := marshalCodexTurnMetadata(metadata)
	if err != nil {
		return nil, "", err
	}
	var tools bytes.Buffer
	// Derive the tool ID from the pinned fixture's compact representation.
	if err := json.Compact(&tools, []byte(gjson.GetBytes(openAIEvalCodexContextJSON, "tools").Raw)); err != nil {
		return nil, "", fmt.Errorf("compact Codex evaluation tools: %w", err)
	}
	space := uuid.NewSHA1(uuid.NameSpaceOID, []byte(session))
	input := []any{
		map[string]any{"type": "additional_tools", "id": "at_" + uuid.NewSHA1(space, tools.Bytes()).String(), "role": "developer", "tools": captured.Tools},
		openAIEvalCodexMessage("msg_"+uuid.NewSHA1(space, []byte(captured.BaseInstructions)).String(), "developer", "", 0,
			openAIEvalCodexText{Kind: "model.base_instructions", Text: captured.BaseInstructions}),
	}
	created := float64(now.UnixMicro()) / 1e6
	for _, message := range captured.Messages {
		input = append(input, openAIEvalCodexMessage("msg_"+uuid.NewString(), message.Role, turn, created, message.Content...))
	}
	environment := fmt.Sprintf("<environment_context>\n  <cwd>/home/user/workspace</cwd>\n  <shell>bash</shell>\n  <current_date>%s</current_date>\n  <timezone>Etc/UTC</timezone>\n  <filesystem><workspace_roots><root>/home/user/workspace</root></workspace_roots><permission_profile type=\"managed\"><file_system type=\"restricted\"><entry access=\"read\"><special>:root</special></entry><entry access=\"write\"><path>/home/user/workspace</path></entry><entry access=\"write\"><special>:slash_tmp</special></entry><entry access=\"write\"><special>:tmpdir</special></entry><entry access=\"read\"><path>/home/user/workspace/.git</path></entry><entry access=\"read\"><path>/home/user/workspace/.agents</path></entry><entry access=\"read\"><path>/home/user/workspace/.codex</path></entry><entry access=\"read\"><path>/home/user/workspace/.aws</path></entry></file_system></permission_profile></filesystem>\n</environment_context>", now.UTC().Format(time.DateOnly))
	input = append(input,
		openAIEvalCodexMessage("msg_"+uuid.NewString(), "user", turn, created, openAIEvalCodexText{Kind: "environments.environment_context", Text: environment}),
		openAIEvalCodexMessage("msg_"+uuid.NewString(), "user", turn, created, openAIEvalCodexText{Kind: "user.text", Text: "Do not use any external tools to answer the following request.\n\n" + prompt}),
	)
	return map[string]any{
		"model": model, "input": input, "tool_choice": "auto", "parallel_tool_calls": false,
		"reasoning": reasoning, "store": false, "stream": true,
		"include": []any{"reasoning.encrypted_content"}, "prompt_cache_key": session,
		"text": map[string]any{"verbosity": "low"},
		"client_metadata": map[string]any{
			"thread_id": session, "x-codex-window-id": window, "turn_id": turn,
			"root_turn_id": turn, openAIWSTurnMetadataHeader: string(turnMetadata),
			"x-codex-installation-id": installation, "session_id": session,
			"x-client-request-id": session,
		},
	}, session, nil
}

func openAIEvalCodexMessage(id, role, turn string, created float64, parts ...openAIEvalCodexText) map[string]any {
	content, kinds := make([]any, 0, len(parts)), make([]any, 0, len(parts))
	for _, part := range parts {
		content = append(content, map[string]any{"type": "input_text", "text": part.Text})
		kinds = append(kinds, part.Kind)
	}
	metadata := map[string]any{"content_item_kinds": kinds}
	if turn != "" {
		metadata["turn_id"] = turn
	}
	if created != 0 {
		metadata["create_time"] = created
	}
	return map[string]any{"type": "message", "id": id, "role": role, "content": content, "internal_chat_message_metadata_passthrough": metadata}
}

// The payload has already been projected once. Copy its final IDs rather than
// scoping the same IDs again in headers; no downstream principal is borrowed.
func applyOpenAIEvalCodexPayloadHeaders(headers http.Header, body []byte) bool {
	cm := gjson.GetBytes(body, "client_metadata")
	if cm.Get("thread_id").String() == "" || cm.Get(openAIWSTurnMetadataHeader).String() == "" {
		return false
	}
	for header, field := range map[string]string{
		"session-id": "session_id", "thread-id": "thread_id", "x-client-request-id": "x-client-request-id",
		"x-codex-installation-id": "x-codex-installation-id", "x-codex-window-id": "x-codex-window-id",
		openAIWSTurnMetadataHeader: openAIWSTurnMetadataHeader,
	} {
		headers.Set(header, cm.Get(field).String())
	}
	headers.Set("session_id", gjson.GetBytes(body, "prompt_cache_key").String())
	headers.Set(responsesLiteHeader, "true")
	headers.Set("x-codex-beta-features", "remote_compaction_v2")
	return true
}

func marshalOpenAIEvalPayload(payload map[string]any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(payload); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}
