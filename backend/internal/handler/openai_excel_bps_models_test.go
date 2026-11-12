package handler

import (
	"errors"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func pinnedBPSModelsAccount() service.Account {
	a := newPinnedCodexAccount(2, service.StatusActive, true, false)
	a.Type = service.AccountTypeOAuth
	a.Credentials = map[string]any{"client_id": openai.ExcelClientID, "model_mapping": map[string]any{"public-astra": "gpt-6-astra", "public-sol": "gpt-6.1-sol"}}
	return a
}

func TestCompositeBPSModelsHandlerAppliesFinalPolicy(t *testing.T) {
	for _, routeFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "mixed transport", true: "route lookup fails"}[routeFailure], func(t *testing.T) {
			bps := pinnedBPSModelsAccount()
			// Composite resolves the public alias before account eligibility;
			// both transports must support the resolved target for a mixed pool.
			bps.Credentials["model_mapping"] = map[string]any{"gpt-6-astra": "gpt-6-astra"}
			native := newPinnedCodexAccount(1, service.StatusActive, true, false)
			upstream := &codexModelsPinnedHTTPUpstream{bodies: map[int64]string{native.ID: `{"models":[{"slug":"public-astra","multi_agent_version":"v2","multi_agent_reasoning_effort":"high"}]}`}}
			h := newPinnedCodexTestHandler([]service.Account{native, bps}, upstream, 3)
			routes := &compositeWSRouteRepo{routes: []service.CompositeModelRoute{{
				ID: 1, GroupID: 77, PublicModel: "public-astra", MatchType: service.CompositeRouteMatchExact,
				TargetPlatform: service.PlatformOpenAI, UpstreamModel: "gpt-6-astra", Endpoint: service.CompositeRouteEndpointResponses, Enabled: true,
			}}}
			if routeFailure {
				routes.err = errors.New("route database unavailable")
			}
			h.compositeResolver = service.NewCompositeRouteResolver(routes)
			group := &service.Group{ID: 77, Platform: service.PlatformComposite}
			rec := performPinnedCodexModelsRequest(t, h, group, "")
			if routeFailure {
				require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
				require.NotContains(t, rec.Body.String(), `"multi_agent_version":"v2"`)
				return
			}
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Equal(t, "null", gjson.GetBytes(rec.Body.Bytes(), "models.0.multi_agent_version").Raw)
			require.Equal(t, "null", gjson.GetBytes(rec.Body.Bytes(), "models.0.multi_agent_reasoning_effort").Raw)
			second := performPinnedCodexModelsRequest(t, h, group, rec.Header().Get("ETag"))
			require.Equal(t, http.StatusNotModified, second.Code)
			require.Empty(t, second.Body.String())
		})
	}
}

func TestPinnedBPSModelsLocalAliasesAndFinalETag(t *testing.T) {
	a := pinnedBPSModelsAccount()
	upstream := &codexModelsPinnedHTTPUpstream{}
	h := newPinnedCodexTestHandler([]service.Account{a}, upstream, 3)
	group := &service.Group{ID: 77, Platform: service.PlatformOpenAI,
		CodexModelsManifestConfig: service.GroupCodexModelsManifestConfig{Enabled: true, AccountIDs: []int64{a.ID}},
		ModelAllowlist:            service.GroupModelAllowlist{Enabled: true, Models: []string{"public-astra"}}}
	rec := performPinnedCodexModelsRequest(t, h, group, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, []string{"public-astra"}, codexHandlerManifestSlugs(t, rec))
	require.Equal(t, "v2", gjson.GetBytes(rec.Body.Bytes(), "models.0.multi_agent_version").String())
	require.Equal(t, gjson.Null, gjson.GetBytes(rec.Body.Bytes(), "models.0.multi_agent_reasoning_effort").Type)
	etag := rec.Header().Get("ETag")
	require.NotEmpty(t, etag)
	repeated := performPinnedCodexModelsRequest(t, h, group, etag)
	require.Equal(t, http.StatusNotModified, repeated.Code)
	require.Empty(t, repeated.Body.String())
	// A different final group filter must invalidate the previous ETag.
	group.ModelAllowlist.Models = []string{"public-sol"}
	changed := performPinnedCodexModelsRequest(t, h, group, etag)
	require.Equal(t, http.StatusOK, changed.Code)
	require.Equal(t, []string{"public-sol"}, codexHandlerManifestSlugs(t, changed))
	require.NotEqual(t, etag, changed.Header().Get("ETag"))
	require.Empty(t, upstream.accountIDs(), "BPS directory generation must not send HTTP")
}

func TestPinnedBPSModelsConfigurationAndAuthoritativeEmpty(t *testing.T) {
	a := pinnedBPSModelsAccount()
	delete(a.Credentials, "model_mapping")
	group := &service.Group{ID: 77, Platform: service.PlatformOpenAI,
		CodexModelsManifestConfig: service.GroupCodexModelsManifestConfig{Enabled: true, AccountIDs: []int64{a.ID}}}
	upstream := &codexModelsPinnedHTTPUpstream{}
	h := newPinnedCodexTestHandler([]service.Account{a}, upstream, 3)
	rec := performPinnedCodexModelsRequest(t, h, group, "")
	require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "explicit model mappings")
	group.ModelAllowlist = service.GroupModelAllowlist{Enabled: true, Models: []string{"gpt-6-astra"}}
	rec = performPinnedCodexModelsRequest(t, h, group, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, []string{"gpt-6-astra"}, codexHandlerManifestSlugs(t, rec))
	// Explicit configuration filtered to nothing is successful and must never
	// pull the unrestricted defaults back into the directory.
	a = pinnedBPSModelsAccount()
	h = newPinnedCodexTestHandler([]service.Account{a}, upstream, 3)
	group.ModelAllowlist.Models = []string{"unmapped-model"}
	rec = performPinnedCodexModelsRequest(t, h, group, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Empty(t, codexHandlerManifestSlugs(t, rec))
	require.Empty(t, upstream.accountIDs())
}

func TestPinnedBPSMixedPoolKeepsPortableCollaboration(t *testing.T) {
	for _, source := range []string{"bps", "native", "both"} {
		t.Run(source, func(t *testing.T) {
			bps := pinnedBPSModelsAccount()
			native := newPinnedCodexAccount(3, service.StatusActive, true, false)
			native.Credentials["model_mapping"] = map[string]any{"public-astra": "gpt-6-astra"}
			upstream := &codexModelsPinnedHTTPUpstream{bodies: map[int64]string{native.ID: `{"models":[{"slug":"gpt-6-astra","multi_agent_version":"v2","multi_agent_reasoning_effort":"high"}]}`}}
			h := newPinnedCodexTestHandler([]service.Account{bps, native}, upstream, 3)
			pins := map[string][]int64{"bps": {bps.ID}, "native": {native.ID}, "both": {bps.ID, native.ID}}[source]
			group := &service.Group{ID: 77, Platform: service.PlatformOpenAI,
				CodexModelsManifestConfig: service.GroupCodexModelsManifestConfig{Enabled: true, AccountIDs: pins},
				ModelAllowlist:            service.GroupModelAllowlist{Enabled: true, Models: []string{"public-astra"}}}
			rec := performPinnedCodexModelsRequest(t, h, group, "")
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Equal(t, []string{"public-astra"}, codexHandlerManifestSlugs(t, rec))
			for _, field := range []string{"multi_agent_version", "multi_agent_reasoning_effort"} {
				value := gjson.GetBytes(rec.Body.Bytes(), "models.0."+field)
				require.True(t, value.Raw == "null", field+": "+value.Raw)
			}
			if source == "bps" {
				require.Empty(t, upstream.accountIDs())
			} else {
				require.Equal(t, []int64{native.ID}, upstream.accountIDs())
			}
		})
	}
}
