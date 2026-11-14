package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func dualBPSEvalFixture(t *testing.T) *Account {
	bps := excelBPSAccount(t)
	return &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"client_id": openai.ClientID, "access_token": "native-test-access", "chatgpt_account_id": "workspace-test", "model_mapping": map[string]any{"alias": "gpt-6-astra"}, ExcelBPSCredentialsKey: bps.Credentials}, Extra: map[string]any{ExcelBPSEnabledKey: true, ExcelBPSModelsKey: []any{"gpt-6-astra"}}}
}
func TestOpenAIEvalDualGrantDiscoveryFollowsMappedChannel(t *testing.T) {
	var nativeRequests []*http.Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nativeRequests = append(nativeRequests, r)
		_, _ = io.WriteString(w, `{"models":[{"slug":"gpt-6-astra"}]}`)
	}))
	defer server.Close()
	original := chatgptCodexModelsURL
	chatgptCodexModelsURL = server.URL
	t.Cleanup(func() { chatgptCodexModelsURL = original })

	for _, scenario := range []string{"bps", "disabled", "unselected"} {
		t.Run(scenario, func(t *testing.T) {
			a := dualBPSEvalFixture(t)
			nativeRequests = nil
			body := `{"allowed":true,"model_catalog":{"models":[{"id":"gpt-6-astra"}]}}`
			if scenario != "bps" {
				body = `{"models":[{"slug":"gpt-6-astra"}]}`
				if scenario == "disabled" {
					a.Extra[ExcelBPSEnabledKey] = false
				} else {
					a.Extra[ExcelBPSModelsKey] = []any{"gpt-6-sol"}
				}
			}
			upstream := &httpUpstreamRecorder{resp: excelBPSMockResponse(200, body)}
			gateway := excelBPSTestService(upstream)
			repo := excelBPSCatalogAccountRepo{account: a}
			gateway.accountRepo = repo
			tests := &AccountTestService{accountRepo: repo, openaiGatewayService: gateway}
			target, err := tests.ResolveOpenAIEvalTarget(context.Background(), a.ID, "alias")
			require.NoError(t, err)
			require.Equal(t, "gpt-6-astra", target.UpstreamModel)
			require.Equal(t, scenario == "bps", target.Credential.IsExcelOAuth())
			require.Equal(t, "native-test-access", a.GetOpenAIAccessToken())
			if scenario == "bps" {
				require.Len(t, upstream.requests, 1)
				require.Empty(t, nativeRequests)
				require.Contains(t, upstream.lastReq.URL.String(), "/basispoints/api/responses/access")
				require.Equal(t, "Bearer "+a.ExcelBPSCredentialAccount().GetOpenAIAccessToken(), upstream.lastReq.Header.Get("Authorization"))
			} else {
				require.Empty(t, upstream.requests)
				require.Len(t, nativeRequests, 1)
				require.Equal(t, "Bearer native-test-access", nativeRequests[0].Header.Get("Authorization"))
			}
		})
	}
}

func TestOpenAIEvalTargetRequiresMappedCatalogEntitlement(t *testing.T) {
	for _, tc := range []struct {
		name, requested, mapped, catalog string
		allowed                          bool
	}{
		{"requested name alone does not authorize mapped target", "gpt-6-astra", "gpt-99", "gpt-6-astra", false},
		{"canonical request may map to an available provider target", "gpt-6-astra", "gpt-99", "gpt-99", true},
		{"public alias may map to an available canonical target", "public-alias", "gpt-6-astra", "gpt-6-astra", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := excelBPSAccount(t)
			a.Credentials["model_mapping"] = map[string]any{tc.requested: tc.mapped}
			upstream := &httpUpstreamRecorder{resp: excelBPSMockResponse(200, `{"allowed":true,"model_catalog":{"models":[{"id":"`+tc.catalog+`"}]}}`)}
			gateway := excelBPSTestService(upstream)
			repo := excelBPSCatalogAccountRepo{account: a}
			gateway.accountRepo = repo
			tests := &AccountTestService{accountRepo: repo, openaiGatewayService: gateway}
			target, err := tests.ResolveOpenAIEvalTarget(t.Context(), a.ID, tc.requested)
			if tc.allowed {
				require.NoError(t, err)
				require.Equal(t, tc.mapped, target.UpstreamModel)
			} else {
				require.Nil(t, target)
				require.ErrorContains(t, err, "not available in the account catalog")
				require.ErrorContains(t, err, tc.mapped)
			}
			require.Len(t, upstream.requests, 1)
			require.Equal(t, http.MethodGet, upstream.requests[0].Method, "entitlement checks must not send inference")
		})
	}
}

func TestOpenAIConnectionDualGrantUsesBPSWithoutMutatingNative(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a := dualBPSEvalFixture(t)
	upstream := &httpUpstreamRecorder{resp: excelBPSMockResponse(200, excelBPSSuccessWire)}
	gateway := excelBPSTestService(upstream)
	tests := &AccountTestService{httpUpstream: upstream, openaiGatewayService: gateway}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/test", nil)
	require.NoError(t, tests.testOpenAIAccountConnection(c, a, "alias", "", AccountTestModeDefault))
	require.Equal(t, basispoints.ResponsesURL, upstream.lastReq.URL.String())
	require.Equal(t, "Bearer "+a.ExcelBPSCredentialAccount().GetOpenAIAccessToken(), upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, "native-test-access", a.GetOpenAIAccessToken())
}

func TestOpenAIConnectionDualGrantImagesUseBPSGenerationEndpoint(t *testing.T) {
	a := dualBPSEvalFixture(t)
	a.Credentials["model_mapping"] = map[string]any{"image-alias": "gpt-image-2"}
	a.Extra[ExcelBPSModelsKey] = []any{"gpt-image-2"}
	recorderUpstream := &httpUpstreamRecorder{resp: excelBPSMockResponse(200, `{"data":[{"b64_json":"aW1hZ2U=","output_format":"png"}]}`)}
	upstream := &excelBPSSingleSendRecorder{recorderUpstream}
	gateway := excelBPSTestService(recorderUpstream)
	tests := &AccountTestService{httpUpstream: upstream, openaiGatewayService: gateway}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/test", nil)
	require.NoError(t, tests.testOpenAIAccountConnection(c, a, "image-alias", "draw", ""))
	require.Equal(t, basispoints.ImagesGenerationsURL, recorderUpstream.lastReq.URL.String())
	require.Contains(t, recorder.Body.String(), `"type":"image"`)
	require.Contains(t, recorder.Body.String(), `"success":true`)
	require.Equal(t, "native-test-access", a.GetOpenAIAccessToken())
}

func TestOpenAIConnectionDualGrantImagesUseAndReleaseConfiguredPoolProxy(t *testing.T) {
	a := dualBPSEvalFixture(t)
	a.Extra[ExcelBPSModelsKey] = []any{"gpt-image-2"}
	a.Extra[ExcelBPSSessionProxyEnabledKey] = true
	a.Extra[ExcelBPSSessionProxyIDsKey] = []any{float64(11)}
	recorderUpstream := &httpUpstreamRecorder{resp: excelBPSMockResponse(200, `{"data":[{"b64_json":"aW1hZ2U=","output_format":"png"}]}`)}
	upstream := &excelBPSSingleSendRecorder{recorderUpstream}
	gateway := excelBPSTestService(recorderUpstream)
	gateway.settingService = &SettingService{proxyRepo: &excelBPSProxyRepoStub{}}
	pool := gateway.excelBPSProxyPoolFor(a.ExcelBPSCredentialAccount())
	proxy := Proxy{ID: 11, Protocol: "http", Host: "127.0.0.1", Port: 9011, Status: StatusActive}
	pool.nodes[11] = &excelBPSProxyNode{proxy: proxy, ready: true}
	tests := &AccountTestService{httpUpstream: upstream, openaiGatewayService: gateway}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/test", nil)
	require.NoError(t, tests.testOpenAIAccountConnection(c, a, "gpt-image-2", "draw", ""))
	require.Equal(t, basispoints.ImagesGenerationsURL, recorderUpstream.lastReq.URL.String())
	require.Equal(t, proxy.URL(), recorderUpstream.lastProxyURL)
	require.Contains(t, recorder.Body.String(), `"success":true`)
	require.Zero(t, pool.nodes[11].active)
	require.Nil(t, a.Proxy, "pool projection must not persist its proxy on the native account")
}

func TestOpenAIConnectionDualGrantUnsupportedImageResolvesBeforeBPSPool(t *testing.T) {
	for _, ready := range []bool{false, true} {
		t.Run(map[bool]string{false: "cold", true: "ready"}[ready], func(t *testing.T) {
			a := dualBPSEvalFixture(t)
			a.Credentials["model_mapping"] = map[string]any{"image-alias": "gpt-image-1"}
			a.Extra[ExcelBPSModelsKey] = []any{"gpt-image-1"}
			a.Extra[ExcelBPSSessionProxyEnabledKey] = true
			a.Extra[ExcelBPSSessionProxyIDsKey] = []any{float64(11)}
			nativeProxy := &Proxy{ID: 12, Protocol: "http", Host: "127.0.0.1", Port: 9012, Status: StatusActive}
			a.ProxyID, a.Proxy = &nativeProxy.ID, nativeProxy
			upstream := &httpUpstreamRecorder{resp: excelBPSMockResponse(400, `{"error":{"message":"native_probe_reached"}}`)}
			gateway := excelBPSTestService(upstream)
			gateway.settingService = &SettingService{proxyRepo: &excelBPSProxyRepoStub{}}
			pool := gateway.excelBPSProxyPoolFor(a.ExcelBPSCredentialAccount())
			pool.nodes[11] = &excelBPSProxyNode{proxy: Proxy{ID: 11, Protocol: "http", Host: "127.0.0.1", Port: 9011, Status: StatusActive}, ready: ready}
			tests := &AccountTestService{httpUpstream: upstream, openaiGatewayService: gateway}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest("POST", "/test", nil)

			_ = tests.testOpenAIAccountConnection(c, a, "image-alias", "draw", "")

			require.Len(t, upstream.requests, 1, recorder.Body.String())
			require.Equal(t, "chatgpt.com", upstream.lastReq.URL.Host)
			require.Equal(t, "Bearer native-test-access", upstream.lastReq.Header.Get("Authorization"))
			require.Equal(t, nativeProxy.URL(), upstream.lastProxyURL)
			require.Contains(t, recorder.Body.String(), "native_probe_reached")
			require.Zero(t, pool.nodes[11].active)
			require.Same(t, nativeProxy, a.Proxy)
			require.Equal(t, "native-test-access", a.GetOpenAIAccessToken())
			require.Equal(t, true, a.Extra[ExcelBPSEnabledKey], "native test projection must not disable the saved BPS route")
		})
	}
}
