package service

import (
	"context"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/prism"
)

// PrismPublicModel uses the common admin model shape plus verified effort metadata.
type PrismPublicModel struct {
	ID            string   `json:"id"`
	Object        string   `json:"object"`
	Type          string   `json:"type"`
	DisplayName   string   `json:"display_name"`
	Efforts       []string `json:"reasoning_efforts,omitempty"`
	DefaultEffort string   `json:"default_reasoning_effort,omitempty"`
}

func PrismPublicModels(account *Account, catalog []prism.UpstreamModel) []PrismPublicModel {
	models := make([]PrismPublicModel, 0, len(catalog))
	if account == nil {
		return models
	}
	mapping := account.GetModelMapping()
	for _, model := range catalog {
		appendModel := func(id string) {
			models = append(models, PrismPublicModel{ID: id, Object: "model", Type: "model", DisplayName: model.Label, Efforts: append([]string(nil), model.Efforts...), DefaultEffort: model.DefaultEffort})
		}
		if len(mapping) == 0 {
			appendModel(model.ID)
			continue
		}
		for alias, target := range mapping {
			if target == model.ID && !strings.ContainsAny(alias, "*?") {
				appendModel(alias)
			}
		}
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models
}

// A Prism listing never falls back to static OpenAI/Claude model names. Each
// account contributes only its current, verified entitlements and valid aliases.
func (s *GatewayService) getAvailablePrismModels(ctx context.Context, groupID *int64) []string {
	if s.prismGateway != nil {
		ctx = withPrismLifecycle(ctx, s.prismGateway.prismAccountService)
	}
	var accounts []Account
	var err error
	if groupID != nil {
		accounts, err = s.accountRepo.ListSchedulableByGroupID(ctx, *groupID)
	} else {
		accounts, err = s.accountRepo.ListSchedulable(ctx)
	}
	if err != nil {
		return nil
	}
	set := make(map[string]bool)
	for i := range accounts {
		a := &accounts[i]
		if a.Platform != PlatformPrism || !a.IsSchedulable() {
			continue
		}
		a, models, catalogErr := PrismAccountCatalogSnapshot(ctx, a)
		if catalogErr != nil {
			continue
		}
		allowed := make(map[string]bool, len(models))
		for _, model := range models {
			allowed[model.ID] = true
		}
		mapping := a.GetModelMapping()
		if len(mapping) == 0 {
			for model := range allowed {
				set[model] = true
			}
		} else {
			for alias, model := range mapping {
				if allowed[model] && !strings.ContainsAny(alias, "*?") {
					set[alias] = true
				}
			}
		}
	}
	models := make([]string, 0, len(set))
	for model := range set {
		models = append(models, model)
	}
	sort.Strings(models)
	return models
}
