package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type excelBPSProxyRepoStub struct {
	ProxyRepository
	proxies []Proxy
	reads   int
}

func (r *excelBPSProxyRepoStub) ListByIDs(context.Context, []int64) ([]Proxy, error) {
	r.reads++
	return r.proxies, nil
}

func TestExcelBPSSessionProxyKeepsParentChildEgressAndWaitsForActiveLease(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a := excelBPSAccount(t)
	a.Extra = map[string]any{ExcelBPSSessionProxyEnabledKey: true, ExcelBPSSessionProxyIDsKey: []any{float64(11), float64(12)}}
	s := &OpenAIGatewayService{settingService: &SettingService{proxyRepo: &excelBPSProxyRepoStub{}}}
	pool := s.excelBPSProxyPoolFor(a)
	for _, id := range []int64{11, 12} {
		pool.nodes[id] = &excelBPSProxyNode{proxy: Proxy{ID: id, Protocol: "http", Host: "127.0.0.1", Port: 9000 + int(id), Status: StatusActive}, ready: true}
	}
	makeContext := func(thread string) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/responses", nil)
		c.Request.Header.Set("session-id", "same-root-session")
		c.Request.Header.Set("thread-id", thread)
		return c
	}
	parent, first, err := s.acquireExcelBPSSessionProxy(context.Background(), makeContext("parent"), a, nil)
	require.NoError(t, err)
	child, second, err := s.acquireExcelBPSSessionProxy(context.Background(), makeContext("child"), a, nil)
	require.NoError(t, err)
	require.Equal(t, *parent.ProxyID, *child.ProxyID)
	require.Nil(t, a.ProxyID, "projection must not mutate account configuration")
	first.release(io.ErrUnexpectedEOF)
	_, _, err = s.acquireExcelBPSSessionProxy(context.Background(), makeContext("parent"), a, nil)
	require.ErrorContains(t, err, "active session request")
	second.release(nil)
	other, third, err := s.acquireExcelBPSSessionProxy(context.Background(), makeContext("child"), a, nil)
	require.NoError(t, err)
	require.NotEqual(t, *parent.ProxyID, *other.ProxyID)
	third.release(nil)
	third.release(nil)
	require.Zero(t, pool.nodes[*other.ProxyID].active)
}

func TestExcelBPSSessionProxyNeverFallsBackToDirectOrColdProbe(t *testing.T) {
	a := excelBPSAccount(t)
	a.Extra = map[string]any{ExcelBPSSessionProxyEnabledKey: true, ExcelBPSSessionProxyIDsKey: []any{float64(11)}}
	s := &OpenAIGatewayService{settingService: &SettingService{proxyRepo: &excelBPSProxyRepoStub{}}}
	_, _, err := s.acquireExcelBPSSessionProxy(context.Background(), nil, a, nil)
	require.ErrorContains(t, err, "background validation")
	pool := s.excelBPSProxyPoolFor(a)
	pool.nodes[11] = &excelBPSProxyNode{proxy: Proxy{ID: 11, Status: StatusActive}, ready: true, cooldown: time.Now().Add(time.Minute)}
	_, _, err = s.acquireExcelBPSSessionProxy(context.Background(), nil, a, nil)
	require.Error(t, err)
	delete(a.Extra, ExcelBPSSessionProxyEnabledKey)
	actual, lease, err := s.acquireExcelBPSSessionProxy(context.Background(), nil, a, nil)
	require.NoError(t, err)
	require.Same(t, a, actual)
	require.Nil(t, lease)
}

func TestExcelBPSSessionProxyWarmsPending403RecoveryOnly(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(map[bool]string{false: "automatic403", true: "manual_disable"}[manual], func(t *testing.T) {
			a := excelBPSAccount(t)
			a.Status = StatusActive
			a.Schedulable = true
			a.Extra = map[string]any{ExcelBPSSessionProxyEnabledKey: true, ExcelBPSSessionProxyIDsKey: []any{float64(11)}, ExcelBPS403DisabledAtKey: time.Now().UTC().Format(time.RFC3339Nano), ExcelBPS403DisabledReasonKey: "upstream_403"}
			if manual {
				a.Extra[ExcelBPSEnabledKey] = false
			}
			repo := &excelBPSProxyRepoStub{proxies: []Proxy{{ID: 11, Protocol: "http", Host: "127.0.0.1", Port: 9011, Status: StatusActive}}}
			upstream := &httpUpstreamRecorder{resp: excelBPSMockResponse(200, `{"allowed":true}`)}
			svc := openAIClientToolsTestService(upstream)
			svc.settingService = &SettingService{proxyRepo: repo}
			svc.warmExcelBPSSessionProxies(context.Background(), a)
			if manual {
				require.Zero(t, repo.reads)
				require.Empty(t, upstream.requests)
			} else {
				require.Equal(t, 1, repo.reads)
				require.Len(t, upstream.requests, 1)
				require.True(t, svc.excelBPSProxyPoolFor(a).nodes[11].ready)
			}
		})
	}
}

func TestExcelBPSSessionProxyKnownUnsentConnectionFailureCoolsNode(t *testing.T) {
	require.True(t, excelBPSProxyStreamFailure(&OpenAIEvalRequestError{Code: "evaluation_connection_not_sent", Message: "Connection failed before request send"}))
	require.False(t, excelBPSProxyStreamFailure(context.Canceled))
	require.False(t, excelBPSProxyStreamFailure(&OpenAIEvalRequestError{Code: "permission_denied", HTTPStatus: 403}))
}

func TestExcelBPSSessionProxyReconcilesUnavailableCachedNodes(t *testing.T) {
	for _, change := range []string{"deleted", "disabled", "expired", "unconfigured"} {
		t.Run(change, func(t *testing.T) {
			a := excelBPSAccount(t)
			a.Status, a.Schedulable = StatusActive, true
			a.Extra = map[string]any{ExcelBPSSessionProxyEnabledKey: true, ExcelBPSSessionProxyIDsKey: []any{float64(11)}}
			proxy := Proxy{ID: 11, Protocol: "http", Host: "127.0.0.1", Port: 9011, Status: StatusActive}
			repo := &excelBPSProxyRepoStub{proxies: []Proxy{proxy}}
			upstream := &httpUpstreamRecorder{resp: excelBPSMockResponse(200, `{"allowed":true}`)}
			svc := openAIClientToolsTestService(upstream)
			svc.settingService = &SettingService{proxyRepo: repo}
			svc.warmExcelBPSSessionProxies(context.Background(), a)
			pool := svc.excelBPSProxyPoolFor(a)
			require.True(t, pool.nodes[11].ready)
			_, lease, err := svc.acquireExcelBPSSessionProxy(context.Background(), nil, a, nil)
			require.NoError(t, err)
			switch change {
			case "deleted":
				repo.proxies = nil
			case "disabled":
				repo.proxies[0].Status = "disabled"
			case "expired":
				expired := time.Now().Add(-time.Minute)
				repo.proxies[0].ExpiresAt = &expired
			case "unconfigured":
				a.Extra[ExcelBPSSessionProxyIDsKey] = []any{float64(12)}
				repo.proxies = nil
			}
			svc.warmExcelBPSSessionProxies(context.Background(), a)
			require.False(t, pool.nodes[11].ready)
			require.Equal(t, 1, pool.nodes[11].active)
			_, _, err = svc.acquireExcelBPSSessionProxy(context.Background(), nil, a, nil)
			require.Error(t, err)
			require.Len(t, upstream.requests, 1, "revoked nodes must not trigger access probes or direct fallback")
			lease.release(nil)
			require.Zero(t, pool.nodes[11].active)
		})
	}
}

func TestExcelBPSSessionProxyAddressChangeWaitsForLeaseDrain(t *testing.T) {
	a := excelBPSAccount(t)
	a.Status, a.Schedulable = StatusActive, true
	a.Extra = map[string]any{ExcelBPSSessionProxyEnabledKey: true, ExcelBPSSessionProxyIDsKey: []any{float64(11)}}
	oldProxy := Proxy{ID: 11, Protocol: "http", Host: "127.0.0.1", Port: 9011, Status: StatusActive}
	newProxy := oldProxy
	newProxy.Port = 9012
	repo := &excelBPSProxyRepoStub{proxies: []Proxy{oldProxy}}
	upstream := &httpUpstreamRecorder{responses: []*http.Response{excelBPSMockResponse(200, `{"allowed":true}`), excelBPSMockResponse(200, `{"allowed":true}`)}}
	svc := openAIClientToolsTestService(upstream)
	svc.settingService = &SettingService{proxyRepo: repo}
	svc.warmExcelBPSSessionProxies(context.Background(), a)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/responses", nil)
	c.Request.Header.Set("session-id", "same-root")
	first, lease, err := svc.acquireExcelBPSSessionProxy(context.Background(), c, a, nil)
	require.NoError(t, err)
	require.Equal(t, oldProxy.URL(), first.Proxy.URL())
	repo.proxies = []Proxy{newProxy}
	svc.warmExcelBPSSessionProxies(context.Background(), a)
	pool := svc.excelBPSProxyPoolFor(a)
	require.Equal(t, oldProxy.URL(), pool.nodes[11].proxy.URL())
	require.False(t, pool.nodes[11].ready)
	require.Len(t, upstream.requests, 1, "new address cannot be warmed while old endpoint has leases")
	_, _, err = svc.acquireExcelBPSSessionProxy(context.Background(), c, a, nil)
	require.ErrorContains(t, err, "active session request")
	_, _, err = svc.acquireExcelBPSSessionProxy(context.Background(), nil, a, nil)
	require.Error(t, err, "other sessions cannot reuse the stale address")
	lease.release(nil)
	require.Zero(t, pool.nodes[11].active)
	svc.warmExcelBPSSessionProxies(context.Background(), a)
	require.Equal(t, newProxy.URL(), upstream.lastProxyURL)
	next, nextLease, err := svc.acquireExcelBPSSessionProxy(context.Background(), c, a, nil)
	require.NoError(t, err)
	require.Equal(t, newProxy.URL(), next.Proxy.URL())
	require.Equal(t, oldProxy.URL(), first.Proxy.URL(), "the in-flight account snapshot is immutable")
	nextLease.release(nil)
	require.Zero(t, pool.nodes[11].active)
}

type excelBPSBlockedProbeBody struct {
	io.Reader
	started chan struct{}
	resume  chan struct{}
	once    sync.Once
}

func (b *excelBPSBlockedProbeBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.started); <-b.resume })
	return b.Reader.Read(p)
}
func (*excelBPSBlockedProbeBody) Close() error { return nil }

func TestExcelBPSSessionProxyIgnoresStaleProbeCompletion(t *testing.T) {
	for _, change := range []string{"deleted", "address_changed", "lease_failure"} {
		t.Run(change, func(t *testing.T) {
			a := excelBPSAccount(t)
			a.Status, a.Schedulable = StatusActive, true
			a.Extra = map[string]any{ExcelBPSSessionProxyEnabledKey: true, ExcelBPSSessionProxyIDsKey: []any{float64(11)}}
			proxy := Proxy{ID: 11, Protocol: "http", Host: "127.0.0.1", Port: 9011, Status: StatusActive}
			repo := &excelBPSProxyRepoStub{proxies: []Proxy{proxy}}
			blocked := &excelBPSBlockedProbeBody{Reader: strings.NewReader(`{"allowed":true}`), started: make(chan struct{}), resume: make(chan struct{})}
			oldResponse := excelBPSMockResponse(200, "")
			oldResponse.Body = blocked
			upstream := &httpUpstreamRecorder{responses: []*http.Response{oldResponse, excelBPSMockResponse(403, `{"allowed":false}`)}}
			svc := openAIClientToolsTestService(upstream)
			svc.settingService = &SettingService{proxyRepo: repo}
			pool := svc.excelBPSProxyPoolFor(a)
			var lease *excelBPSProxyLease
			if change == "lease_failure" {
				pool.nodes[11] = &excelBPSProxyNode{proxy: proxy, ready: true}
				var err error
				_, lease, err = svc.acquireExcelBPSSessionProxy(context.Background(), nil, a, nil)
				require.NoError(t, err)
			}
			done := make(chan struct{})
			go func() { defer close(done); svc.warmExcelBPSSessionProxies(context.Background(), a) }()
			select {
			case <-blocked.started:
			case <-time.After(3 * time.Second):
				t.Fatal("probe did not start")
			}
			switch change {
			case "deleted":
				repo.proxies = nil
			case "address_changed":
				repo.proxies[0].Port = 9012
			case "lease_failure":
				lease.release(io.ErrUnexpectedEOF)
			}
			svc.warmExcelBPSSessionProxies(context.Background(), a)
			close(blocked.resume)
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("stale probe did not finish")
			}
			require.False(t, pool.nodes[11].ready, "old successful access probe cannot undo revocation or a newer failure")
			if change == "address_changed" {
				require.Equal(t, 9012, pool.nodes[11].proxy.Port)
			}
			if change == "lease_failure" {
				require.Equal(t, 1, pool.nodes[11].failures)
				require.True(t, pool.nodes[11].cooldown.After(time.Now()))
				require.Zero(t, pool.nodes[11].active)
			}
			_, _, err := svc.acquireExcelBPSSessionProxy(context.Background(), nil, a, nil)
			require.Error(t, err)
		})
	}
}
