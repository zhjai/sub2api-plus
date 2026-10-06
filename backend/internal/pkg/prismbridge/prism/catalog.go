package prism

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// PathInferenceModels is the account-specific directory used by Prism's UI.
const PathInferenceModels = "/api/inference/models"

type UpstreamModel struct {
	ID            string   `json:"id"`
	Label         string   `json:"label"`
	Efforts       []string `json:"reasoning_efforts,omitempty"`
	DefaultEffort string   `json:"default_reasoning_effort,omitempty"`
}

func (c *Client) InferenceModels(ctx context.Context, p Principal) ([]UpstreamModel, error) {
	_, _, raw, err := c.doJSON(ctx, p, http.MethodGet, PathInferenceModels, nil, nil, "application/json")
	if err != nil {
		return nil, err
	}
	return ParseModelCatalog(raw)
}

// A valid empty array means no entitlement; malformed responses must never
// become an apparently successful empty catalog or a fabricated static list.
func ParseModelCatalog(raw []byte) ([]UpstreamModel, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '{' {
		var wrapped map[string]json.RawMessage
		if err := json.Unmarshal(raw, &wrapped); err != nil {
			return nil, fmt.Errorf("invalid model catalog: %w", err)
		}
		var ok bool
		raw, ok = wrapped["models"]
		if !ok {
			raw, ok = wrapped["data"]
		}
		if !ok {
			return nil, errors.New("model catalog missing array")
		}
		raw = bytes.TrimSpace(raw)
	}
	if len(raw) == 0 || raw[0] != '[' {
		return nil, errors.New("model catalog is not an array")
	}
	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("invalid model catalog: %w", err)
	}
	out := make([]UpstreamModel, 0, len(list))
	seen := make(map[string]bool, len(list))
	for _, item := range list {
		var m map[string]any
		if json.Unmarshal(item, &m) != nil {
			continue
		}
		id, _ := m["id"].(string)
		label, _ := m["label"].(string)
		id, label = strings.TrimSpace(id), strings.TrimSpace(label)
		if id == "" || label == "" || seen[id] {
			continue
		}
		seen[id] = true
		um := UpstreamModel{ID: id, Label: label}
		for _, k := range []string{"reasoning_efforts", "supported_reasoning_efforts", "reasoning_effort_options"} {
			if um.Efforts = effortValues(m[k]); len(um.Efforts) > 0 {
				break
			}
		}
		if d, _ := m["default_reasoning_effort"].(string); d != "" {
			um.DefaultEffort = strings.TrimSpace(d)
		}
		out = append(out, um)
	}
	if len(list) > 0 && len(out) == 0 {
		return nil, errors.New("model catalog has no valid entries")
	}
	return out, nil
}

func effortValues(v any) []string {
	arr, _ := v.([]any)
	out := []string{}
	seen := map[string]bool{}
	for _, e := range arr {
		var s string
		switch t := e.(type) {
		case string:
			s = t
		case map[string]any:
			for _, k := range []string{"value", "effort", "id"} {
				if s, _ = t[k].(string); s != "" {
					break
				}
			}
		}
		if s = strings.TrimSpace(s); s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
