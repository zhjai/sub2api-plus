package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/prism"
)

func TestPrismCodexHydrationUsesRefreshedAccountGeneration(t *testing.T) {
	old := Account{ID: 7, Platform: PlatformPrism, Credentials: map[string]any{"access_token": "old", "model_mapping": map[string]any{"public": "old-model"}}}
	fresh := Account{ID: 7, Platform: PlatformPrism, Credentials: map[string]any{"access_token": "new", "model_mapping": map[string]any{"public": "new-model"}}}
	got, err := hydratePrismCodexAccount(context.Background(), old,
		func(context.Context, *Account) (*Account, error) { return &fresh, nil },
		func(ctx context.Context, a *Account) ([]prism.UpstreamModel, error) {
			if a.GetMappedModel("public") != "new-model" || a.GetCredential("access_token") != "new" {
				t.Fatal("catalog received stale generation")
			}
			if _, ok := ctx.Value(prismLifecycleContextKey{}).(*PrismAccountService); ok {
				t.Fatal("catalog could invisibly refresh again")
			}
			return []prism.UpstreamModel{{ID: "new-model", Efforts: []string{"high"}, DefaultEffort: "high"}}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	metadata, ok := groupCodexModelMetadata(PlatformPrism, "public", []Account{got}, nil, nil, true)
	if !ok || metadata.DefaultReasoningLevel != "high" || got.GetMappedModel("public") != "new-model" {
		t.Fatalf("stale metadata: %+v", metadata)
	}
	if old.GetMappedModel("public") != "old-model" {
		t.Fatal("original account snapshot mutated")
	}
	models := []prism.UpstreamModel{{ID: "new-model", Efforts: []string{"high"}, DefaultEffort: "high"}}
	model, effort, err := resolvePrismAccountSnapshotModel(&got, models, "public", "")
	if err != nil || model != "new-model" || effort != "high" {
		t.Fatalf("mixed resolution generation: %s %s %v", model, effort, err)
	}
	if _, _, err := resolvePrismAccountSnapshotModel(&got, models, "public", "low"); err == nil {
		t.Fatal("stale effort accepted")
	}
	public := PrismPublicModels(&got, models)
	if len(public) != 1 || public[0].ID != "public" || public[0].DefaultEffort != "high" {
		t.Fatalf("mixed projection generation: %+v", public)
	}
}

func TestPrismCodexManifestLiveCapabilities(t *testing.T) {
	for _, name := range []string{"prism-custom-model", "gpt-6-astra"} {
		a := prismCodexAccountMetadata(Account{ID: 1, Platform: PlatformPrism}, []prism.UpstreamModel{{ID: name, Efforts: []string{"high", "xhigh"}, DefaultEffort: "high"}})
		b := prismCodexAccountMetadata(Account{ID: 2, Platform: PlatformPrism}, []prism.UpstreamModel{{ID: name, Efforts: []string{"high"}, DefaultEffort: "high"}})
		body, err := buildCodexModelsManifestForAccounts(PlatformPrism, []string{name}, []Account{a, b}, &Group{Platform: PlatformPrism}, nil, true)
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Models []struct {
				Default string `json:"default_reasoning_level"`
				Levels  []struct {
					Effort string `json:"effort"`
				} `json:"supported_reasoning_levels"`
				Input   []string `json:"input_modalities"`
				Search  bool     `json:"supports_search_tool"`
				Tiers   []any    `json:"service_tiers"`
				Compact any      `json:"auto_compact_token_limit"`
			} `json:"models"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Models) != 1 {
			t.Fatalf("models: %s", body)
		}
		m := result.Models[0]
		if m.Default != "high" || len(m.Levels) != 1 || m.Levels[0].Effort != "high" || len(m.Input) != 1 || m.Input[0] != "text" || m.Search || len(m.Tiers) != 0 || m.Compact != nil {
			t.Fatalf("invented Prism capabilities: %s", body)
		}
	}
}

func TestPrismCodexMissingCatalogCannotInventEfforts(t *testing.T) {
	a := Account{Platform: PlatformPrism}
	metadata, ok := groupCodexModelMetadata(PlatformPrism, "gpt-6-astra", []Account{a}, nil, nil, true)
	if !ok || !metadata.prism || !metadata.reasoningConflict {
		t.Fatalf("missing directory did not fail closed: %+v", metadata)
	}
	d := newConfiguredCodexModelDescriptor("gpt-6-astra")
	applyUpstreamModelMetadataToCodexDescriptor(&d, metadata)
	if d.DefaultReasoningLevel != nil || len(d.SupportedReasoningLevels) != 0 {
		t.Fatal("static reasoning defaults survived missing catalog")
	}
}
