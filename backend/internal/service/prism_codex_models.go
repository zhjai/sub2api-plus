package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/prism"
)

func prismCodexAccountMetadata(account Account, models []prism.UpstreamModel) Account {
	extra := make(map[string]any, len(account.Extra)+1)
	for key, value := range account.Extra {
		extra[key] = value
	}
	snapshot := UpstreamModelMetadataSnapshot{Source: "prism_live_catalog", Models: map[string]UpstreamModelMetadata{}}
	for _, model := range models {
		// An empty effort directory is unknown, not permission to invent none/low.
		reasoning := true
		snapshot.Models[model.ID] = UpstreamModelMetadata{ID: model.ID, DisplayName: model.Label,
			Reasoning: &reasoning, DefaultReasoningLevel: model.DefaultEffort,
			SupportedReasoningLevels: append([]string(nil), model.Efforts...), InputModalities: []string{"text"},
			CodexToolCapabilities: map[string]json.RawMessage{"service_tiers": json.RawMessage("[]"), "supports_search_tool": json.RawMessage("false"), "use_responses_lite": json.RawMessage("false"), "comp_hash": json.RawMessage("null")}}
	}
	extra[UpstreamModelMetadataExtraKey] = snapshot
	account.Extra = extra
	return account
}

func (s *GatewayService) hydratePrismCodexAccounts(ctx context.Context, accounts []Account) ([]Account, error) {
	if s.prismGateway != nil {
		ctx = withPrismLifecycle(ctx, s.prismGateway.prismAccountService)
	}
	out := append([]Account(nil), accounts...)
	for i := range out {
		if out[i].Platform != PlatformPrism {
			continue
		}
		fresh, models, err := PrismAccountCatalogSnapshot(ctx, &out[i])
		if err != nil {
			return nil, fmt.Errorf("Prism model capabilities unavailable: %w", err)
		}
		out[i] = prismCodexAccountMetadata(*fresh, models)
	}
	return out, nil
}

func hydratePrismCodexAccount(ctx context.Context, account Account, refresh func(context.Context, *Account) (*Account, error), catalog func(context.Context, *Account) ([]prism.UpstreamModel, error)) (Account, error) {
	fresh, models, err := prismAccountCatalogSnapshot(ctx, &account, refresh, catalog)
	if err != nil {
		return Account{}, err
	}
	return prismCodexAccountMetadata(*fresh, models), nil
}
