package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// This body intentionally ignores the HTTP request context. The adapter must
// close it when its own context is cancelled, including custom transports.
type excelBPSBlockingBody struct {
	reading             chan struct{}
	closed              chan struct{}
	readOnce, closeOnce sync.Once
	closeCalls          atomic.Int32
}

type excelBPSCatalogAccountRepo struct {
	AccountRepository
	account *Account
}

func (r excelBPSCatalogAccountRepo) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}

func TestOpenAIExcelBPSLocalModelDiscovery(t *testing.T) {
	a := excelBPSAccount(t)
	a.Status, a.Schedulable = StatusActive, true
	a.Credentials["model_mapping"] = map[string]any{"public-astra": "gpt-6-astra"}
	upstream := &httpUpstreamRecorder{}
	svc := openAIClientToolsTestService(upstream)
	svc.accountRepo = splitCodexModelsAccountRepo{catalog: map[int64][]Account{77: {*a}}}
	_, err := svc.FetchOpenAIModelsList(context.Background(), a)
	require.Error(t, err, "live discovery must not masquerade as configured local models")
	picker, err := (&AccountTestService{openaiGatewayService: svc}).FetchOpenAIAccountModels(context.Background(), a)
	require.NoError(t, err)
	require.Len(t, picker, 1)
	require.Equal(t, "public-astra", picker[0].ID, "BPS aliases must not be projected twice")
	// Evaluation preflight uses configured BPS models without live discovery.
	a.Credentials["model_mapping"] = map[string]any{"gpt-6-astra": "gpt-6-astra"}
	eval := &AccountTestService{accountRepo: excelBPSCatalogAccountRepo{account: a}, openaiGatewayService: svc}
	target, err := eval.ResolveOpenAIEvalTarget(context.Background(), a.ID, "gpt-6-astra")
	require.NoError(t, err)
	require.Equal(t, "gpt-6-astra", target.RequestedModel)
	a.Credentials["model_mapping"] = map[string]any{"public-astra": "gpt-6-astra"}
	group := &Group{ID: 77, Platform: PlatformOpenAI,
		CodexModelsManifestConfig: GroupCodexModelsManifestConfig{Enabled: true, AccountIDs: []int64{a.ID}},
		ModelAllowlist:            GroupModelAllowlist{Enabled: true, Models: []string{"public-astra"}}}
	pinned, selected, err := svc.FetchPinnedOpenAIModelsList(context.Background(), group, 3, "")
	require.NoError(t, err)
	require.Equal(t, a.ID, selected.ID)
	require.Equal(t, "public-astra", gjson.GetBytes(pinned.Body, "data.0.id").String())
	require.Equal(t, int64(1), gjson.GetBytes(pinned.Body, "data.#").Int())
	unchanged, _, err := svc.FetchPinnedOpenAIModelsList(context.Background(), group, 3, pinned.ETag)
	require.NoError(t, err)
	require.True(t, unchanged.NotModified)
	require.Empty(t, upstream.requests)

	t.Run("wildcard needs a concrete selection", func(t *testing.T) {
		b := *excelBPSAccount(t)
		b.Credentials["model_mapping"] = map[string]any{"gpt-*": "gpt-6-astra"}
		_, err := buildExcelBPSModelsManifest(&b, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "explicit model mappings")
		manifest, err := buildExcelBPSModelsManifest(&b, &Group{ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{"gpt-6.1-sol"}}})
		require.NoError(t, err)
		require.Equal(t, "gpt-6.1-sol", gjson.GetBytes(manifest.Body, "models.0.slug").String())
	})
	// Native live discovery remains explicitly unsupported for BPS: the local
	// catalog must never be mistaken for a fetched entitlement manifest.
	_, err = svc.FetchCodexModelsManifest(context.Background(), a, "", "")
	require.Error(t, err)
	require.Empty(t, upstream.requests)
}

func TestOpenAIExcelBPSCompositeManifestPolicy(t *testing.T) {
	for _, scenario := range []string{"mixed", "bps", "native", "undeclared"} {
		t.Run(scenario, func(t *testing.T) {
			bps := *excelBPSAccount(t)
			native := Account{ID: 43, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
			accounts := []Account{bps, native}
			if scenario == "bps" {
				accounts = []Account{bps}
			}
			if scenario == "native" {
				accounts = []Account{native}
			}
			original := []byte(`{"models":[{"slug":"public-astra","multi_agent_version":"v2","multi_agent_reasoning_effort":"high"}]}`)
			if scenario == "undeclared" {
				original = []byte(`{"models":[{"slug":"public-astra"}]}`)
			}
			svc := &OpenAIGatewayService{
				accountRepo: codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{77: accounts}},
			}
			resolver := NewCompositeRouteResolver(compositeRouteRepoStub{routes: []CompositeModelRoute{{
				ID: 1, GroupID: 77, PublicModel: "public-astra", MatchType: CompositeRouteMatchExact,
				TargetPlatform: PlatformOpenAI, UpstreamModel: "gpt-6-astra", Endpoint: CompositeRouteEndpointResponses, Enabled: true,
			}}})
			manifest := &OpenAIModelsResponse{Body: original, ETag: codexModelsManifestBodyETag(original)}
			group := &Group{ID: 77, Platform: PlatformComposite}
			require.NoError(t, svc.RestrictCompositeBPSModelsManifest(context.Background(), group, manifest, resolver, ""))
			if scenario == "mixed" {
				require.Equal(t, "null", gjson.GetBytes(manifest.Body, "models.0.multi_agent_version").Raw)
				require.Equal(t, "null", gjson.GetBytes(manifest.Body, "models.0.multi_agent_reasoning_effort").Raw)
			} else if scenario == "bps" {
				require.Equal(t, "v2", gjson.GetBytes(manifest.Body, "models.0.multi_agent_version").String())
				require.Equal(t, "null", gjson.GetBytes(manifest.Body, "models.0.multi_agent_reasoning_effort").Raw)
			} else {
				require.Equal(t, original, manifest.Body)
			}
			etag := manifest.ETag
			require.Equal(t, codexModelsManifestBodyETag(manifest.Body), etag)
			require.NoError(t, svc.RestrictCompositeBPSModelsManifest(context.Background(), group, manifest, resolver, etag))
			require.True(t, manifest.NotModified)
			require.Empty(t, manifest.Body)
		})
	}
}

func newExcelBPSBlockingBody() *excelBPSBlockingBody {
	return &excelBPSBlockingBody{reading: make(chan struct{}), closed: make(chan struct{})}
}

func (b *excelBPSBlockingBody) Read([]byte) (int, error) {
	b.readOnce.Do(func() { close(b.reading) })
	<-b.closed
	return 0, io.EOF
}

func (b *excelBPSBlockingBody) Close() error {
	b.closeCalls.Add(1)
	b.closeOnce.Do(func() { close(b.closed) })
	return nil
}

func TestOpenAIExcelBPSForwardCancellationClosesBridge(t *testing.T) {
	body := newExcelBPSBlockingBody()
	t.Cleanup(func() { _ = body.Close() })
	resp := excelBPSMockResponse(200, "")
	resp.Body = body
	upstream := &httpUpstreamRecorder{resp: resp}
	s := openAIClientToolsTestService(upstream)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	// Deliberately distinct: honour the forwarder's context, including its
	// billing/drain policy, rather than replacing it with the Gin context.
	c.Request = httptest.NewRequest("POST", "/responses", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	account := excelBPSAccount(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = s.Forward(ctx, c, account, []byte(`{"model":"gpt-6-astra","input":"hello","stream":true}`))
	}()
	select {
	case <-body.reading:
	case <-time.After(3 * time.Second):
		t.Fatal("bridge never started reading")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("forwarding did not stop after cancellation")
	}
	require.Len(t, upstream.requests, 1)
	require.Equal(t, int32(1), body.closeCalls.Load())
	select {
	case <-body.closed:
	default:
		t.Fatal("upstream body was not closed")
	}
}

func TestOpenAIExcelBPSTestCancellationClosesBridge(t *testing.T) {
	for _, evaluation := range []bool{false, true} {
		t.Run(map[bool]string{false: "connection", true: "evaluation"}[evaluation], func(t *testing.T) {
			body := newExcelBPSBlockingBody()
			t.Cleanup(func() { _ = body.Close() })
			resp := excelBPSMockResponse(200, "")
			resp.Body = body
			upstream := &excelBPSSingleSendRecorder{&httpUpstreamRecorder{resp: resp}}
			s := &AccountTestService{httpUpstream: upstream}
			a := excelBPSAccount(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			req := httptest.NewRequest("POST", chatgptCodexAPIURL, strings.NewReader(`{"model":"gpt-6-astra","input":"hello","stream":true}`)).WithContext(ctx)
			req.Header.Set("Authorization", "Bearer "+a.GetOpenAIAccessToken())
			var err error
			if evaluation {
				resp, err = s.doOpenAIEvalUpstream(req, "", a, a)
			} else {
				resp, err = s.doOpenAIAccountTestUpstream(req, "", a, false)
			}
			require.NoError(t, err)
			defer resp.Body.Close()
			done := make(chan struct{})
			go func() { _, _ = io.ReadAll(resp.Body); close(done) }()
			select {
			case <-body.reading:
			case <-time.After(3 * time.Second):
				t.Fatal("bridge never started reading")
			}
			cancel()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("test bridge did not stop after cancellation")
			}
			require.Len(t, upstream.requests, 1)
		})
	}
}

func TestOpenAIExcelBPSRuntimeRoutingEligibility(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformComposite} {
		for _, layout := range []string{"bps exact native default", "bps wildcard native exact", "bps default native exact"} {
			t.Run(platform+"/"+layout, func(t *testing.T) {
				bps := *excelBPSAccount(t)
				native := Account{ID: 43, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{}}
				bps.Credentials["model_mapping"] = map[string]any{"gpt-6-astra": "gpt-6-astra"}
				if layout != "bps exact native default" {
					native.Credentials["model_mapping"] = map[string]any{"gpt-6-astra": "gpt-6-astra"}
					if layout == "bps wildcard native exact" {
						bps.Credentials["model_mapping"] = map[string]any{"gpt-*": "gpt-6-astra"}
					} else {
						delete(bps.Credentials, "model_mapping")
					}
				}
				routes := []CompositeModelRoute{{PublicModel: "gpt-6-astra", TargetPlatform: PlatformOpenAI, Endpoint: CompositeRouteEndpointResponses, Enabled: true}}
				body, _, err := restrictExcelBPSModels([]byte(`{"models":[{"slug":"gpt-6-astra","multi_agent_version":"v2","multi_agent_reasoning_effort":"xhigh"}]}`), platform, []Account{bps, native}, &Group{Platform: platform}, routes, true)
				require.NoError(t, err)
				require.Equal(t, "null", gjson.GetBytes(body, "models.0.multi_agent_version").Raw)
				require.Equal(t, "null", gjson.GetBytes(body, "models.0.multi_agent_reasoning_effort").Raw)
			})
		}
	}
	t.Run("account ownership remains exclusive", func(t *testing.T) {
		bps := *excelBPSAccount(t)
		bps.Credentials["model_mapping"] = map[string]any{"quality-alias": "gpt-6-astra"}
		native := Account{ID: 43, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{}}
		body, _, err := restrictExcelBPSModels([]byte(`{"models":[{"slug":"quality-alias","multi_agent_version":"v2","multi_agent_reasoning_effort":"xhigh"}]}`), PlatformComposite, []Account{bps, native}, &Group{Platform: PlatformComposite}, nil, true)
		require.NoError(t, err)
		require.Equal(t, "v2", gjson.GetBytes(body, "models.0.multi_agent_version").String())
		require.Equal(t, "null", gjson.GetBytes(body, "models.0.multi_agent_reasoning_effort").Raw)
	})
	t.Run("other endpoint does not shadow Responses", func(t *testing.T) {
		bps := *excelBPSAccount(t)
		delete(bps.Credentials, "model_mapping")
		routes := []CompositeModelRoute{{PublicModel: "gpt-6-astra", TargetPlatform: PlatformAnthropic, Endpoint: CompositeRouteEndpointChatCompletions, Enabled: true}}
		body, _, err := restrictExcelBPSModels([]byte(`{"models":[{"slug":"gpt-6-astra","multi_agent_version":"v2","multi_agent_reasoning_effort":"xhigh"}]}`), PlatformComposite, []Account{bps}, &Group{Platform: PlatformComposite}, routes, true)
		require.NoError(t, err)
		require.Equal(t, "null", gjson.GetBytes(body, "models.0.multi_agent_reasoning_effort").Raw)
	})
}

func TestOpenAIExcelBPSGeneratedCompositeCatalogFailsClosed(t *testing.T) {
	bps := *excelBPSAccount(t)
	bps.Status, bps.Schedulable = StatusActive, true
	for _, scenario := range []string{"missing resolver", "route failure", "account failure"} {
		t.Run(scenario, func(t *testing.T) {
			svc := &GatewayService{accountRepo: codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{77: {bps}}}}
			if scenario == "route failure" {
				svc.compositeResolver = NewCompositeRouteResolver(excelBPSFailingRoutes{})
			}
			if scenario == "account failure" {
				svc.accountRepo = &countingCodexModelsAccountRepo{err: errors.New("unavailable")}
			}
			body, err := svc.BuildCodexModelsManifestForGroup(context.Background(), &Group{ID: 77, Platform: PlatformComposite}, "", []string{"gpt-6-astra"})
			require.Error(t, err)
			require.Equal(t, http.StatusServiceUnavailable, infraerrors.Code(err))
			require.Empty(t, body)
		})
	}
}

type excelBPSFailingRoutes struct{ CompositeModelRouteRepository }

func (excelBPSFailingRoutes) ListByGroup(context.Context, int64, bool) ([]CompositeModelRoute, error) {
	return nil, errors.New("unavailable")
}
