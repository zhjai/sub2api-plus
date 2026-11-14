package basispoints

import "github.com/tidwall/gjson"

// NativeFallbackReason reports whole-turn capabilities absent from this bridge.
// tool_choice:none disables declarations only, not existing hosted dependencies.
func NativeFallbackReason(body []byte) string {
	if !gjson.ValidBytes(body) {
		return ""
	}
	choice := gjson.GetBytes(body, "tool_choice")
	if isUnsupportedHostedTool(choice.Get("type").String()) {
		return "tool_choice"
	}
	var inspect func(gjson.Result) string
	inspect = func(items gjson.Result) string {
		for _, item := range items.Array() {
			kind := item.Get("type").String()
			if isUnsupportedHostedTool(kind) {
				return kind
			}
			if kind == "namespace" {
				if found := inspect(item.Get("tools")); found != "" {
					return found
				}
			}
		}
		return ""
	}
	disabled := choice.Type == gjson.String && choice.String() == "none"
	if !disabled {
		if found := inspect(gjson.GetBytes(body, "tools")); found != "" {
			return found
		}
	}
	for _, item := range gjson.GetBytes(body, "input").Array() {
		switch item.Get("type").String() {
		case "web_search_call", "image_generation_call", "file_search_call", "computer_call", "computer_call_output", "code_interpreter_call":
			return "input.hosted_tool_dependency"
		case "additional_tools", "tool_search_output":
			if !disabled {
				if found := inspect(item.Get("tools")); found != "" {
					return found
				}
			}
		}
	}
	return ""
}
