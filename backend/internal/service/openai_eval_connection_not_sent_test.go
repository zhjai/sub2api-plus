//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

func TestOpenAIEvalTypedDialFailureWithoutTraceRefundsOnce(t *testing.T) {
	for _, scenario := range []string{"dial", "dns", "connected_dial", "partial_write", "response", "generic_timeout", "read_timeout"} {
		t.Run(scenario, func(t *testing.T) {
			spy := &evalBudgetSpy{}
			controller := &openAIEvalController{store: spy, owner: "test", namespace: "test"}
			ctx := context.WithValue(t.Context(), openAIEvalControllerKey{}, controller)
			req, _ := http.NewRequestWithContext(ctx, "POST", "https://invalid.test/private?token=secret", nil)
			req, finish := openAIEvalTrackPhysicalSend(req)
			pending := req.Context().Value(openAIEvalPhysicalSendKey{}).(*openAIEvalPhysicalSend)
			pending.prepared.Store(true)
			trace := httptrace.ContextClientTrace(req.Context())
			var response *http.Response
			var transportErr error = &url.Error{Op: "Post", URL: "https://user:password@invalid.test/?token=private", Err: &net.OpError{Op: "dial", Net: "tcp", Addr: &net.TCPAddr{IP: net.ParseIP("198.18.7.40"), Port: 443}, Err: context.DeadlineExceeded}}
			action := "refund"
			switch scenario {
			case "dns":
				transportErr = &net.DNSError{Err: "lookup failed", Name: "private.internal"}
			case "connected_dial":
				trace.GotConn(httptrace.GotConnInfo{})
				action = "uncertain"
			case "partial_write":
				trace.WroteRequest(httptrace.WroteRequestInfo{Err: io.ErrUnexpectedEOF})
				action = "uncertain"
			case "response":
				response = &http.Response{StatusCode: 403}
				action = "confirm"
			case "generic_timeout":
				transportErr = errors.New("dial tcp timeout: untyped text")
				action = "uncertain"
			case "read_timeout":
				transportErr = &net.OpError{Op: "read", Err: context.DeadlineExceeded}
				action = "uncertain"
			}
			classified := finish(response, transportErr)
			_ = finish(response, transportErr)
			require.Equal(t, []string{action}, spy.operations)
			if action == "refund" {
				var failure *OpenAIEvalRequestError
				require.ErrorAs(t, classified, &failure)
				require.Equal(t, "evaluation_connection_not_sent", failure.Code)
				require.False(t, failure.Attempted)
				require.False(t, failure.Retryable)
				require.NotContains(t, failure.Message, "password")
				require.NotContains(t, failure.Message, "198.18.7.40")
				require.NotContains(t, failure.Message, "private")
				require.Contains(t, failure.Message, "proxy")
				require.Empty(t, openAIEvalRunDiagnostics(ctx).DeferredReason)
			} else {
				require.Same(t, transportErr, classified)
			}
		})
	}
}

type evalBPSDialUpstream struct {
	HTTPUpstream
	calls int
	proxy string
	trace bool
}

func (*evalBPSDialUpstream) SupportsSingleSend() bool { return true }
func (s *evalBPSDialUpstream) Do(req *http.Request, proxy string, _ int64, _ int) (*http.Response, error) {
	s.calls++
	s.proxy = proxy
	s.trace = httptrace.ContextClientTrace(req.Context()) != nil
	return nil, &url.Error{Op: "Post", URL: req.URL.String(), Err: &net.OpError{Op: "dial", Net: "tcp", Err: context.DeadlineExceeded}}
}
func (s *evalBPSDialUpstream) DoWithTLS(req *http.Request, proxy string, id int64, c int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return s.Do(req, proxy, id, c)
}
func TestOpenAIEvalBPSUsesConfiguredPoolAndDialFailureDoesNotRetry(t *testing.T) {
	a := excelBPSAccount(t)
	a.Extra = map[string]any{ExcelBPSSessionProxyEnabledKey: true, ExcelBPSSessionProxyIDsKey: []any{float64(11)}}
	upstream := &evalBPSDialUpstream{}
	gateway := excelBPSTestService(&httpUpstreamRecorder{})
	gateway.httpUpstream = upstream
	gateway.settingService = &SettingService{proxyRepo: &excelBPSProxyRepoStub{}}
	pool := gateway.excelBPSProxyPoolFor(a)
	pool.nodes[11] = &excelBPSProxyNode{proxy: Proxy{ID: 11, Protocol: "http", Host: "127.0.0.1", Port: 9011, Status: StatusActive}, ready: true}
	svc := &AccountTestService{httpUpstream: upstream, openaiGatewayService: gateway}
	target := &OpenAIEvalTarget{Account: a, Credential: a, RequestedModel: "gpt-6-astra", UpstreamModel: "gpt-6-astra"}
	_, record, err := svc.RunOpenAIEvalSampleAttempts(t.Context(), target, "reply with 21", "high", 3)
	require.Error(t, err)
	require.Equal(t, "evaluation_connection_not_sent", record.ErrorCode)
	require.Zero(t, record.Attempts)
	require.Len(t, record.AttemptErrors, 1)
	require.Equal(t, 1, upstream.calls)
	require.Equal(t, "http://127.0.0.1:9011", upstream.proxy)
	require.True(t, upstream.trace, "BPS request rebuilding must preserve trace context")
	require.Zero(t, pool.nodes[11].active)
	require.Nil(t, a.ProxyID)
	require.False(t, strings.Contains(record.ErrorMessage, "[url redacted]"), "the actionable classification must replace opaque raw URL failures")
}

func TestOpenAIEvalConnectionNotSentIsDiagnosticOnlyNotQualityFailure(t *testing.T) {
	svc, _, upstream := evalRunHarness(t, func(*http.Request, int) (*http.Response, error) {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: context.DeadlineExceeded}
	})
	run, err := svc.Run(t.Context(), OpenAIEvalRunRequest{AccountID: 995, RequestedModel: "gpt-5.4", TestType: OpenAIEvalTypeCandy, SampleCount: 1}, 1, "manual")
	require.NoError(t, err)
	require.Equal(t, "evaluation_connection_not_sent", run.Error)
	require.Equal(t, "inconclusive", run.Status)
	require.True(t, run.DiagnosticOnly)
	require.Equal(t, "disabled", run.Outcome.Scheduling)
	require.Zero(t, run.RequestCount)
	require.EqualValues(t, 1, upstream.calls.Load())
	repo := svc.accounts.(*openAIAccountTestRepo)
	require.Nil(t, repo.updatedExtra)
	require.Zero(t, repo.setErrorID)
	require.Zero(t, repo.rateLimitedID)
}
