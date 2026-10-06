package service

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var openAIVAPIReplayIDParam = regexp.MustCompile(`^input(?:\[(\d+)\]|\.(\d+)|(\*+))\.id$`)

func isOpenAIReplayItemIDRejection(status int, body []byte) bool {
	if status != http.StatusBadRequest {
		return false
	}
	return openAIReplayItemIDRejectionFields(
		strings.TrimSpace(gjson.GetBytes(body, "error.type").String()),
		strings.TrimSpace(extractUpstreamErrorCode(body)),
		strings.TrimSpace(gjson.GetBytes(body, "error.param").String()),
	)
}

func openAIReplayItemIDRejectionFields(errorType, code, param string) bool {
	errorType = strings.ToLower(strings.TrimSpace(errorType))
	code = strings.ToLower(strings.TrimSpace(code))
	param = strings.ToLower(strings.TrimSpace(param))
	match := openAIVAPIReplayIDParam.FindStringSubmatch(param)
	if match == nil {
		return false
	}
	return (errorType == "v_api_biz_error" && code == "invalid_request") ||
		(errorType == "invalid_request_error" && code == "invalid_value")
}

// Item IDs are optional in a complete stateless replay, but references and
// encrypted reasoning depend on upstream identity and must never be rewritten.
func repairOpenAIVAPIRejectedReplayIDs(body, responseBody []byte, code, param string) ([]byte, bool, error) {
	if !gjson.ValidBytes(body) {
		return nil, false, nil
	}
	if !openAIReplayItemIDRejectionFields(strings.TrimSpace(gjson.GetBytes(responseBody, "error.type").String()), code, param) {
		return nil, false, nil
	}
	param = strings.ToLower(strings.TrimSpace(param))
	match := openAIVAPIReplayIDParam.FindStringSubmatch(param)
	previous := gjson.GetBytes(body, "previous_response_id")
	if match == nil || (previous.Exists() && (previous.Type != gjson.String || strings.TrimSpace(previous.String()) != "")) {
		return nil, false, nil
	}
	conversation := gjson.GetBytes(body, "conversation")
	if conversation.Exists() && conversation.Type != gjson.Null && (conversation.Type != gjson.String || strings.TrimSpace(conversation.String()) != "") {
		return nil, false, nil
	}
	// v-api sometimes reports the rejected field as input*****.id. That
	// spelling does not identify the offending item. Do not guess by removing
	// every item ID: IDs can be meaningful for tool calls, references, and
	// provider-side lineage. A later attempt may still surface the original
	// error with its full param/message for the caller to diagnose.
	if match[3] != "" {
		return nil, false, nil
	}
	input := parseRawJSONView(body).Get("input")
	if !input.IsArray() {
		return nil, false, nil
	}
	items := input.Array()
	for _, item := range items {
		if !item.IsObject() || item.Get("encrypted_content").Exists() {
			return nil, false, nil
		}
		switch item.Get("type").String() {
		case "message", "function_call", "function_call_output", "custom_tool_call", "custom_tool_call_output", "tool_search_call", "tool_search_output":
		default:
			return nil, false, nil
		}
	}
	value := match[1]
	if value == "" {
		value = match[2]
	}
	index, err := strconv.Atoi(value)
	if err != nil || index < 0 || index >= len(items) {
		return nil, false, nil
	}
	rebuilt := make([]string, len(items))
	changed := false
	for i, item := range items {
		rebuilt[i] = item.Raw
		if i != index || !item.Get("id").Exists() {
			continue
		}
		switch item.Get("type").String() {
		case "message", "function_call", "function_call_output", "custom_tool_call", "custom_tool_call_output", "tool_search_call", "tool_search_output":
			updated, err := sjson.Delete(item.Raw, "id")
			if err != nil {
				return nil, false, fmt.Errorf("remove rejected replay item ID: %w", err)
			}
			rebuilt[i], changed = updated, true
		}
	}
	if !changed {
		return nil, false, nil
	}
	return replaceOpenAIRawInput(body, input, rebuilt), true, nil
}
