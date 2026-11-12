package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A model name does not prove that an intermediate API gateway can service
// Codex encrypted collaboration. In particular, an underlying BPS route
// explicitly advertises no multi-agent-v2 capability.
func TestCustomAPIKeyCatalogDoesNotInventEncryptedCollaboration(t *testing.T) {
	for _, modelID := range []string{"gpt-6-astra", "gpt-6.1-sol"} {
		t.Run(modelID, func(t *testing.T) {
			account := Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
				"base_url":      "https://relay.example/v1",
				"model_mapping": map[string]any{"public-model": modelID},
			}}
			for _, capability := range []string{"unknown", "unsupported", "supported"} {
				t.Run(capability, func(t *testing.T) {
					candidate := account
					if capability != "unknown" {
						version, effort := json.RawMessage("null"), json.RawMessage("null")
						if capability == "supported" {
							version, effort = json.RawMessage(`"v2"`), json.RawMessage(`"high"`)
						}
						candidate.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{
							modelID: {ID: modelID, CodexToolCapabilities: map[string]json.RawMessage{
								"multi_agent_version": version, "multi_agent_reasoning_effort": effort,
							}},
						}})
					}
					body, err := buildCodexModelsManifestForAccounts(PlatformOpenAI, []string{"public-model"}, []Account{candidate}, nil, nil, true)
					require.NoError(t, err)
					model := decodeCodexManifestModels(t, body)[0]
					if capability == "supported" {
						require.Equal(t, "v2", model["multi_agent_version"])
						require.Equal(t, "high", model["multi_agent_reasoning_effort"])
					} else {
						require.Contains(t, model, "multi_agent_version")
						require.Nil(t, model["multi_agent_version"])
						require.Nil(t, model["multi_agent_reasoning_effort"])
					}
				})
			}
		})
	}
}

func TestLiveAPIKeyManifestKeepsExplicitNativeCollaboration(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"base_url": "https://relay.example/v1",
	}}
	for _, declaration := range []string{`"multi_agent_version":null,"multi_agent_reasoning_effort":null`, `"multi_agent_version":"v2","multi_agent_reasoning_effort":"high"`} {
		source := []byte(`{"models":[{"slug":"gpt-6-astra",` + declaration + `}]}`)
		body, err := completeAPIKeyCodexModelsManifestMetadata(source, true, account)
		require.NoError(t, err)
		var expected map[string]any
		require.NoError(t, json.Unmarshal([]byte(`{`+declaration+`}`), &expected))
		model := decodeCodexManifestModels(t, body)[0]
		for key, value := range expected {
			require.Contains(t, model, key)
			require.Equal(t, value, model[key])
		}
	}
}

func TestExplicitModelSyncDiscoversActualCollaborationCapabilities(t *testing.T) {
	for _, declaration := range []string{`null`, `"v2"`} {
		t.Run(declaration, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{responses: []*http.Response{
				{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"gpt-6-astra"}]}`))},
				{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"models":[{"slug":"gpt-6-astra","reasoning":true,"supported_reasoning_levels":[{"effort":"high"}],"context_window":256000,"input_modalities":["text"],"multi_agent_version":` + declaration + `,"multi_agent_reasoning_effort":null}]}`))},
			}}
			repo := &upstreamModelMetadataRepoStub{}
			svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream, cfg: upstreamModelSyncTestConfig(), openaiGatewayService: openAIClientToolsTestService(upstream)}
			account := &Account{ID: 44, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://relay.example/v1", "api_key": "synthetic"}}
			catalog, err := svc.SyncUpstreamModelCatalog(context.Background(), account)
			require.NoError(t, err)
			require.Len(t, upstream.requests, 2)
			require.NotEmpty(t, upstream.requests[1].URL.Query().Get("client_version"))
			require.JSONEq(t, declaration, string(catalog.Metadata["gpt-6-astra"].CodexToolCapabilities["multi_agent_version"]))
			snapshot := account.GetUpstreamModelMetadataSnapshot()
			require.NotNil(t, snapshot)
			require.JSONEq(t, declaration, string(snapshot.Models["gpt-6-astra"].CodexToolCapabilities["multi_agent_version"]))
		})
	}
}

func TestModelSyncPersistsCapabilityOnlyRestrictions(t *testing.T) {
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"gpt-6-astra","multi_agent_version":null,"multi_agent_reasoning_effort":null}]}`))},
		{StatusCode: 503, Body: io.NopCloser(strings.NewReader(`{"error":"registry unavailable"}`))},
	}}
	repo := &upstreamModelMetadataRepoStub{}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream, cfg: upstreamModelSyncTestConfig()}
	account := &Account{ID: 45, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic", "base_url": "https://relay.example/v1"}}
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{"gpt-6-astra": {ID: "gpt-6-astra", ContextWindow: 256000, CodexToolCapabilities: map[string]json.RawMessage{"multi_agent_version": json.RawMessage(`"v2"`)}}}})
	_, err := svc.SyncUpstreamModelCatalog(context.Background(), account)
	require.NoError(t, err)
	snapshot := account.GetUpstreamModelMetadataSnapshot()
	require.NotNil(t, snapshot)
	require.JSONEq(t, "null", string(snapshot.Models["gpt-6-astra"].CodexToolCapabilities["multi_agent_version"]))
	require.Equal(t, int64(256000), snapshot.Models["gpt-6-astra"].ContextWindow)
	require.NotEmpty(t, repo.updates)
}

func TestPinnedModelUnionIntersectsCollaborationDeclarations(t *testing.T) {
	for _, second := range []string{`"multi_agent_version":null,"multi_agent_reasoning_effort":null`, `"display_name":"second"`} {
		first := []byte(`{"models":[{"slug":"gpt-6-astra","display_name":"first","multi_agent_version":"v2","multi_agent_reasoning_effort":"high"}]}`)
		next := []byte(`{"models":[{"slug":"gpt-6-astra",` + second + `}]}`)
		for _, order := range [][][]byte{{first, next}, {next, first}} {
			body, err := mergeCodexModelsManifestBodies(order)
			require.NoError(t, err)
			model := decodeCodexManifestModels(t, body)[0]
			require.Contains(t, model, "multi_agent_version")
			require.Nil(t, model["multi_agent_version"])
			require.Nil(t, model["multi_agent_reasoning_effort"])
		}
	}
}
