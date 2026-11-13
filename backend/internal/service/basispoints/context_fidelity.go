package basispoints

import (
	"fmt"
	"strings"
)

// Keep namespace annotations with each selected declaration so replay precedence
// and cache reconstruction cannot detach a tool from its enclosing instructions.
func namespaceMetadata(item object) (object, error) {
	metadata := object{"type": "namespace", "name": item["name"]}
	if value, exists := item["description"]; exists {
		if _, ok := value.(string); !ok {
			return nil, fmt.Errorf("basispoints namespace description must be a string")
		}
		metadata["description"] = value
	}
	return metadata, nil
}

func (t tool) catalogDeclaration() any {
	var declaration any = t.Catalog
	for index := len(t.Namespaces) - 1; index >= 0; index-- {
		wrapper := make(object, len(t.Namespaces[index])+1)
		for key, value := range t.Namespaces[index] {
			wrapper[key] = value
		}
		wrapper["tools"] = []any{declaration}
		declaration = wrapper
	}
	if len(t.Namespaces) == 0 && t.Namespace != "" {
		declaration = object{"type": "namespace", "name": t.Namespace, "tools": []any{declaration}}
	}
	return declaration
}

func namespaceDescriptions(entry object) []string {
	var descriptions []string
	chain, _ := entry["namespaces"].([]object)
	var path string
	for _, namespace := range chain {
		if path != "" {
			path += "."
		}
		path += text(namespace["name"])
		if description := text(namespace["description"]); description != "" {
			descriptions = append(descriptions, "Client namespace "+quoted(path)+": "+description)
		}
	}
	return descriptions
}

// Native reasoning ciphertext remains untouched. Readable summaries become
// explicitly attributed assistant history, which also works without ciphertext.
// Do not infer readable text from opaque fields or silently skip unknown parts.
func reasoningHistoryItems(item object, index int) ([]any, error) {
	var result []any
	if encrypted := text(item["encrypted_content"]); encrypted != "" {
		result = append(result, object{"type": "reasoning", "summary": []any{}, "encrypted_content": encrypted})
	}
	if item["summary"] == nil {
		return result, nil
	}
	summary, ok := item["summary"].([]any)
	if !ok {
		return nil, fmt.Errorf("basispoints reasoning summary must be an array (path=input[%d].summary)", index)
	}
	parts := []any{object{"type": "output_text", "text": "Historical assistant reasoning summary (context only):", "annotations": []any{}}}
	for partIndex, raw := range summary {
		part, _ := raw.(object)
		value, ok := part["text"].(string)
		if text(part["type"]) != "summary_text" || !ok {
			return nil, fmt.Errorf("basispoints reasoning summary requires summary_text (path=input[%d].summary[%d])", index, partIndex)
		}
		parts = append(parts, object{"type": "output_text", "text": value, "annotations": []any{}})
	}
	if len(parts) > 1 {
		result = append(result, object{"type": "message", "role": "assistant", "content": parts})
	}
	return result, nil
}

// Advertise unsupported request semantics without sending unverified fields to
// BPS. Warnings contain field names only, never user values or private payloads.
func unsupportedControlWarnings(source object, effort string) []string {
	var fields []string
	for _, key := range []string{"max_output_tokens", "max_tool_calls", "temperature", "top_p", "truncation", "service_tier", "include"} {
		if value, exists := source[key]; exists && value != nil {
			fields = append(fields, key)
		}
	}
	if reasoning, ok := source["reasoning"].(object); ok && reasoning["summary"] != nil {
		fields = append(fields, "reasoning.summary")
	}
	if config, ok := source["text"].(object); ok && config["verbosity"] != nil {
		fields = append(fields, "text.verbosity")
	}
	var warnings []string
	if len(fields) > 0 {
		warnings = append(warnings, "Basispoints does not apply these request controls: "+strings.Join(fields, ", "))
	}
	if normalized := strings.ToLower(strings.TrimSpace(effort)); normalized == "none" || normalized == "minimal" {
		warnings = append(warnings, "Basispoints maps reasoning effort none/minimal to low; disabling reasoning is not supported")
	}
	return warnings
}
