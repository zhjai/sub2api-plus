package service

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"time"
)

var codexCollaborationCapabilityFields = []string{"multi_agent_version", "multi_agent_reasoning_effort"}

// Apply capability intersection to merged/pinned aliases too. Explicit null
// never becomes v2 merely because another account or an offline template has it.
func restrictCodexCollaborationToGroup(body, upstreamBody []byte, accounts []Account, group *Group) ([]byte, bool, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, false, err
	}
	var models []map[string]json.RawMessage
	if err := json.Unmarshal(envelope["models"], &models); err != nil {
		return nil, false, err
	}
	// Preserve declarations from the selected live/pinned upstream. Only
	// synthesized aliases require an account/group capability inference.
	var upstream struct {
		Models []struct{ Slug string }
	}
	if err := json.Unmarshal(upstreamBody, &upstream); err != nil {
		return nil, false, err
	}
	known := make(map[string]bool, len(upstream.Models))
	for _, model := range upstream.Models {
		known[model.Slug] = true
	}
	changed := false
	for _, model := range models {
		var slug string
		if json.Unmarshal(model["slug"], &slug) != nil || strings.TrimSpace(slug) == "" {
			continue
		}
		if known[slug] {
			continue
		}
		metadata, ok := groupCodexModelMetadata(PlatformOpenAI, slug, accounts, group, nil, true)
		if !ok {
			continue
		}
		for _, field := range codexCollaborationCapabilityFields {
			if !bytes.Equal(bytes.TrimSpace(metadata.CodexToolCapabilities[field]), []byte("null")) {
				continue
			}
			if !bytes.Equal(bytes.TrimSpace(model[field]), []byte("null")) {
				model[field] = json.RawMessage("null")
				changed = true
			}
		}
	}
	if !changed {
		return body, false, nil
	}
	var err error
	envelope["models"], err = json.Marshal(models)
	if err != nil {
		return nil, false, err
	}
	out, err := json.Marshal(envelope)
	return out, err == nil, err
}

// Explicit administrator model synchronization is the discovery boundary.
// Listing configured/pinned catalogs must never probe extra accounts.
func (s *AccountTestService) syncCodexCollaborationMetadata(ctx context.Context, account *Account, metadata map[string]UpstreamModelMetadata, modelIDs []string) {
	if s == nil || s.openaiGatewayService == nil || account == nil || !account.IsOpenAIApiKey() || isOfficialOpenAIModelsBaseURL(account.GetOpenAIBaseURL()) {
		return
	}
	missing := false
	for _, id := range modelIDs {
		for _, field := range codexCollaborationCapabilityFields {
			if len(metadata[id].CodexToolCapabilities[field]) == 0 {
				missing = true
			}
		}
	}
	if !missing {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	manifest, err := s.openaiGatewayService.FetchCodexModelsManifest(ctx, account, CodexCanonicalClientVersion(), "")
	if err != nil || manifest == nil || manifest.NotModified {
		return
	}
	source := manifest.upstreamSourceBody
	if len(source) == 0 {
		source = manifest.Body
	}
	_, live, err := extractUpstreamModelCatalog(source, false)
	if err != nil {
		return
	}
	for _, id := range modelIDs {
		entry, exists := live[id]
		if !exists {
			continue
		}
		current, _ := mergeUpstreamModelMetadata(metadata[id], entry)
		if current.CodexToolCapabilities == nil {
			current.CodexToolCapabilities = make(map[string]json.RawMessage)
		}
		// Only actual upstream fields count. The completed display manifest can
		// contain local defaults and must not become capability evidence.
		applyCodexToolCapabilities(current.CodexToolCapabilities, entry.CodexToolCapabilities, true)
		metadata[id] = current
	}
}
