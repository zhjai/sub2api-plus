package basispoints

import (
	"encoding/json"
	"errors"
)

var ErrInvalidRecoveryRequest = errors.New("invalid prepared Basispoints recovery request")

// WithoutEncryptedReasoning removes only opaque historical reasoning items
// from an already validated wire request. It never walks message content,
// client tool arguments/results, agent assignments or compaction records.
// Readable reasoning summaries have already been preserved as assistant text.
func WithoutEncryptedReasoning(prepared []byte) ([]byte, bool, error) {
	var source object
	if err := decode(prepared, &source); err != nil || source == nil {
		return nil, false, ErrInvalidRecoveryRequest
	}
	input, ok := source["input"].([]any)
	if !ok {
		return prepared, false, nil
	}
	output := make([]any, 0, len(input))
	changed := false
	for _, raw := range input {
		item, _ := raw.(object)
		if text(item["type"]) == "reasoning" && text(item["encrypted_content"]) != "" {
			changed = true
			copyItem := make(object, len(item))
			for k, v := range item {
				if k != "encrypted_content" {
					copyItem[k] = v
				}
			}
			summary, _ := copyItem["summary"].([]any)
			if len(summary) > 0 {
				output = append(output, copyItem)
			}
		} else {
			output = append(output, raw)
		}
	}
	if !changed {
		return prepared, false, nil
	}
	source["input"] = output
	raw, err := json.Marshal(source)
	return raw, true, err
}
