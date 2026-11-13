package basispoints

import "fmt"

// RejectUnsupportedHostedTools inspects declarations, never user text or
// parameter schemas. Namespace children retain the same capability policy.
func RejectUnsupportedHostedTools(raw []byte) error {
	var source object
	if err := decode(raw, &source); err != nil || source == nil {
		return fmt.Errorf("invalid Basispoints request JSON")
	}
	var inspect func(any) error
	inspect = func(value any) error {
		tools, _ := value.([]any)
		for _, entry := range tools {
			tool, _ := entry.(object)
			kind := text(tool["type"])
			if isUnsupportedHostedTool(kind) {
				return fmt.Errorf("hosted tool %q is not available; use a separate native account or select omission", kind)
			}
			if kind == "namespace" {
				if err := inspect(tool["tools"]); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := inspect(source["tools"]); err != nil {
		return err
	}
	// Codex can introduce declarations through these explicit history items.
	input, _ := source["input"].([]any)
	for _, entry := range input {
		item, _ := entry.(object)
		if kind := text(item["type"]); kind == "additional_tools" || kind == "tool_search_output" {
			if err := inspect(item["tools"]); err != nil {
				return err
			}
		}
	}
	return nil
}
