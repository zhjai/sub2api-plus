package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/prism"
	"github.com/stretchr/testify/require"
)

func TestPrismRankingDiscoveryUsesVerifiedPublicModels(t *testing.T) {
	eval, repo, accounts, _ := rankingHarness(t)
	a := prismDispatchFixture(t)
	a.GroupIDs = []int64{77}
	a.Credentials["model_mapping"] = map[string]any{"public-alias": "real-model", "invented-alias": "missing-model"}
	accounts.items = []Account{a}
	key := prismAccountFingerprint(&a)
	prismCatalogCache.Lock()
	prismCatalogCache.entries[key] = prismCatalogEntry{models: []prism.UpstreamModel{{ID: "real-model", Label: "Real", Efforts: []string{"high"}, DefaultEffort: "high"}}, expires: time.Now().Add(time.Minute)}
	prismCatalogCache.Unlock()
	eval.ranking.groups = &rankingTestGroups{items: []Group{{ID: 77, Platform: PlatformPrism, Status: StatusActive}}}
	scopes, coverage, err := eval.ranking.discover(context.Background(), &repo.config, nil)
	require.NoError(t, err)
	require.True(t, coverage.DiscoveryComplete)
	for _, scope := range scopes {
		if scope.group.ID != 77 {
			continue
		}
		require.Contains(t, scope.models, "public-alias")
		require.NotContains(t, scope.models, "real-model")
		require.NotContains(t, scope.models, "invented-alias")
		require.NotContains(t, scope.models, "gpt-5.5")
		return
	}
	t.Fatal("Prism group was not discovered")
}
