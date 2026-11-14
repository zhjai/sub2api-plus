package basispoints

import (
	"fmt"
	"strings"
)

// ContextCapabilityError is a local, pre-send decision, never an instruction
// to replay an upstream rejection. Param contains protocol paths, not payloads.
type ContextCapabilityError struct {
	Code                    string
	Param                   string
	Reason                  string
	Historical              bool
	RequiresCompatibleState bool
}

func (e *ContextCapabilityError) Error() string {
	return fmt.Sprintf("%s (param=%s); use a compatible channel with the complete context or resend the original plaintext", e.Reason, e.Param)
}

func nonemptyEncryption(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case string:
		return v != ""
	case []any:
		return len(v) > 0
	case []string:
		return len(v) > 0
	default:
		return true
	}
}

// InspectRequestCapabilities checks actual protocol values. Merely naming v2
// or including an encrypted schema annotation does not establish ciphertext.
// Native reasoning and compaction state are retained as upstream opaque state.
func InspectRequestCapabilities(raw []byte) *ContextCapabilityError {
	var source object
	if decode(raw, &source) != nil || source == nil {
		return nil
	}
	items, _ := source["input"].([]any)
	if text(source["previous_response_id"]) != "" {
		return &ContextCapabilityError{Code: "basispoints_context_reference", Param: "previous_response_id", Reason: "The BPS bridge requires expanded history for this continuation", RequiresCompatibleState: true}
	}
	boundary := historicalBoundary(items)
	opaque := false
	for i, entry := range items {
		item, _ := entry.(object)
		kind := text(item["type"])
		if kind == "item_reference" {
			return &ContextCapabilityError{Code: "basispoints_context_reference", Param: fmt.Sprintf("input[%d]", i), Reason: "The BPS bridge cannot resolve an upstream item reference without its complete original item", RequiresCompatibleState: true}
		}
		if (kind == "reasoning" || kind == "compaction") && nonemptyEncryption(item["encrypted_content"]) {
			opaque = true
		}
	}
	for i, entry := range items {
		item, _ := entry.(object)
		kind := text(item["type"])
		if (kind == "function_call" || kind == "custom_tool_call") && nonemptyEncryption(item["encrypted_function_args"]) {
			return &ContextCapabilityError{Code: "basispoints_encrypted_arguments", Param: fmt.Sprintf("input[%d].encrypted_function_args", i), Reason: "The bridge cannot translate genuinely encrypted tool arguments", RequiresCompatibleState: true}
		}
		path := ""
		if kind == "agent_message" && nonemptyEncryption(item["encrypted_content"]) {
			path = fmt.Sprintf("input[%d].encrypted_content", i)
		}
		for _, field := range []string{"content", "output"} {
			parts, _ := item[field].([]any)
			for j, partRaw := range parts {
				part, _ := partRaw.(object)
				if text(part["type"]) == "encrypted_content" {
					path = fmt.Sprintf("input[%d].%s[%d]", i, field, j)
					break
				}
			}
		}
		if path != "" {
			return &ContextCapabilityError{Code: "basispoints_encrypted_context", Param: path, Reason: "The bridge has no verified plaintext for this encrypted context", Historical: safelyHistoricalItem(items, i, boundary), RequiresCompatibleState: true}
		}
	}
	if reason := NativeFallbackReason(raw); reason != "" {
		return &ContextCapabilityError{Code: "basispoints_hosted_capability", Param: reason, Reason: "This turn requires a hosted capability absent from the BPS bridge", RequiresCompatibleState: opaque}
	}
	return nil
}

// A later user turn establishes a history boundary. System/developer messages,
// assignments and unmatched calls/results retain their dependency semantics.
func historicalBoundary(items []any) int {
	boundary := -1
	for i, raw := range items {
		item, _ := raw.(object)
		if text(item["role"]) == "user" {
			boundary = i
		}
	}
	return boundary
}

func safelyHistoricalItem(items []any, index, boundary int) bool {
	if boundary < 0 || index >= boundary {
		return false
	}
	item, _ := items[index].(object)
	if role := text(item["role"]); role == "system" || role == "developer" {
		return false
	}
	switch text(item["type"]) {
	case "compaction", "compaction_trigger", "function_call", "custom_tool_call":
		return false
	case "agent_message":
		// Explicit task deliveries remain instructions, even across later turns.
		if text(item["message_type"]) == "NEW_TASK" {
			return false
		}
		parts, _ := item["content"].([]any)
		if strings.Contains(text(item["content"]), "Message Type: NEW_TASK") {
			return false
		}
		for _, raw := range parts {
			part, _ := raw.(object)
			if strings.Contains(text(part["text"]), "Message Type: NEW_TASK") {
				return false
			}
		}
	case "function_call_output", "custom_tool_call_output":
		id := text(item["call_id"])
		if id == "" {
			return false
		}
		for j := 0; j < index; j++ {
			call, _ := items[j].(object)
			if isTool(call) && text(call["call_id"]) == id {
				return true
			}
		}
		return false
	}
	return true
}
