package basispoints

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

// PrepareCompaction adds the real upstream trigger and disables executable
// tools during this phase only. It does not summarize or prune the input.
func PrepareCompaction(raw []byte) ([]byte, error) {
	var source object
	if decode(raw, &source) != nil || source == nil {
		return nil, fmt.Errorf("invalid Basispoints compact request JSON")
	}
	var input []any
	switch value := source["input"].(type) {
	case string:
		input = []any{message("user", value)}
	case []any:
		input = value
	default:
		return nil, fmt.Errorf("Basispoints compact requires complete input history")
	}
	var ordered []any
	var trigger any = object{"type": "compaction_trigger"}
	for _, entry := range input {
		item, _ := entry.(object)
		if text(item["type"]) == "compaction_trigger" {
			trigger = item
		} else {
			ordered = append(ordered, entry)
		}
	}
	source["input"] = append(ordered, trigger)
	source["tool_choice"] = "none"
	return json.Marshal(source)
}

func IsCompactionRequest(raw []byte) bool {
	var source object
	if decode(raw, &source) != nil {
		return false
	}
	input, _ := source["input"].([]any)
	for _, entry := range input {
		item, _ := entry.(object)
		if text(item["type"]) == "compaction_trigger" {
			return true
		}
	}
	return false
}

// ReadCompaction requires a successful terminal and retains the complete real
// upstream window. Some servers omit output on the terminal, so completed
// output-item events are reconstructed by their original output_index.
func ReadCompaction(reader io.Reader, observe func([]byte)) (map[string]any, error) {
	var response object
	completed := false
	items := make(map[int]any)
	progressiveUsage := object{}
	err := readEvents(io.LimitReader(reader, 32<<20), func(event string, raw []byte) error {
		if string(raw) == "[DONE]" {
			return nil
		}
		var p object
		if err := decode(raw, &p); err != nil {
			return err
		}
		if observe != nil {
			observe(raw)
		}
		kind := text(p["type"])
		if kind == "" {
			kind = event
		}
		item, _ := p["item"].(object)
		if isTool(item) || isToolEvent(kind) {
			return fmt.Errorf("compaction returned an executable tool event")
		}
		if kind == "response.output_item.done" {
			index, ok := p["output_index"].(json.Number)
			if !ok || item == nil {
				return fmt.Errorf("compaction returned an invalid output item")
			}
			i, err := index.Int64()
			if err != nil || i < 0 || i > 100000 {
				return fmt.Errorf("compaction returned an invalid output index")
			}
			items[int(i)] = item
		}
		r, _ := p["response"].(object)
		u, _ := r["usage"].(object)
		if u == nil {
			u, _ = p["usage"].(object)
		}
		copyNonzeroImageUsage(progressiveUsage, u)
		switch kind {
		case "response.completed", "response.failed", "response.cancelled", "response.incomplete", "error":
			response = r
			completed = kind == "response.completed"
			return io.EOF
		}
		return nil
	})
	if response != nil {
		u, _ := response["usage"].(object)
		if !hasImageUsage(u) {
			response["usage"] = progressiveUsage
		}
	}
	if err != nil && err != io.EOF {
		return response, err
	}
	if !completed || response == nil || text(response["status"]) != "completed" {
		return response, fmt.Errorf("compaction did not complete")
	}
	if _, exists := response["output"]; !exists && len(items) > 0 {
		indices := make([]int, 0, len(items))
		for index := range items {
			indices = append(indices, index)
		}
		sort.Ints(indices)
		output := make([]any, 0, len(indices))
		for expected, index := range indices {
			if expected != index {
				return response, fmt.Errorf("compaction returned an incomplete output window")
			}
			output = append(output, items[index])
		}
		response["output"] = output
	}
	if _, _, err := CompactWindow(response); err != nil {
		return response, err
	}
	return response, nil
}
