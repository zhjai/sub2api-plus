//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	hcplugin "github.com/hashicorp/go-plugin"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// This fixture returns exactly one queued response per call. Real production
// transmission counts are covered by repository socket tests.
func (*queuedHTTPUpstream) SupportsSingleSend() bool { return true }

func TestR13EvalUnknownTransportAndExactPluginRoute(t *testing.T) {
	for _, pluginMatch := range []bool{false, true} {
		upstream := &hardRPMUpstream{}
		svc, target := evalOAuthHarness(upstream)
		manager := &PluginManager{}
		percent := 0
		if pluginMatch {
			percent = 100
		}
		manager.route.Store(&pluginRoute{pluginID: 7, rolloutPercent: percent})
		svc.pluginManager = manager
		_, record, err := svc.runOpenAIEvalSampleAttempts(context.Background(), target, "OK", "", 3)
		var failure *OpenAIEvalRequestError
		require.ErrorAs(t, err, &failure)
		require.Equal(t, "single_send_unsupported", failure.Code)
		require.False(t, failure.Retryable)
		require.False(t, failure.Attempted)
		require.Zero(t, record.Attempts)
		require.Zero(t, upstream.sends)
		probe := svc.RunOpenAIStateProbe(context.Background(), target)
		require.Zero(t, probe.RequestCount)
		require.Equal(t, "single_send_unsupported", probe.Failure)
	}
	// A configured plugin with no matching rollout must reach the capable
	// transport. Presence of a manager alone must not reject evaluation.
	upstream := &evalTransportStub{respond: func(*http.Request, int) (*http.Response, error) { return nil, errors.New("fixture reached") }}
	svc, target := evalOAuthHarness(upstream)
	svc.pluginManager = &PluginManager{}
	svc.pluginManager.route.Store(&pluginRoute{pluginID: 7, rolloutPercent: 0})
	_, err := svc.RunOpenAIEvalSample(context.Background(), target, "OK", "")
	require.ErrorContains(t, err, "fixture reached")
	require.EqualValues(t, 1, upstream.calls.Load())
}

func TestEvalOAuthRPMWithoutSelectedPluginUsesNativeAdmission(t *testing.T) {
	for _, name := range []string{"plus", "pro"} {
		for _, binding := range []string{"none", "inactive"} {
			t.Run(name+"/"+binding, func(t *testing.T) {
				upstream := &evalTransportStub{respond: func(req *http.Request, _ int) (*http.Response, error) {
					require.True(t, HTTPUpstreamSingleSendRequired(req.Context()))
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}},
						Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":" + evalCompletedJSON("21") + "}\n\n"))}, nil
				}}
				svc, target := evalOAuthHarness(upstream)
				target.Account.Name = name
				target.Account.Extra = map[string]any{"rpm_limit": 1}
				cache := &hardRPMCache{}
				svc.openaiGatewayService.cache = cache
				svc.pluginManager = &PluginManager{}
				if binding == "inactive" {
					svc.pluginManager.route.Store(&pluginRoute{pluginID: 7, rolloutPercent: 0})
				}

				response, attempts, err := svc.RunOpenAIEvalSampleAttempts(t.Context(), target, "probe", "medium", 3)
				require.NoError(t, err)
				require.Equal(t, "21", response.Text)
				require.Equal(t, 1, attempts.Attempts)
				require.EqualValues(t, 1, upstream.calls.Load())
				require.Equal(t, 1, cache.used)

				_, err = svc.runOpenAIEvalSampleSingleSend(t.Context(), target, "probe", "medium")
				require.True(t, IsAccountRPMError(err), "RPM denial must remain intact: %v", err)
				require.EqualValues(t, 1, upstream.calls.Load())
				require.Equal(t, 1, cache.used)
			})
		}
	}
}

type r13WirePlugin struct {
	pluginv1.UnimplementedTransportPluginServer
	target string
	starts atomic.Int64
}

func (p *r13WirePlugin) Forward(stream pluginv1.TransportPlugin_ForwardServer) error {
	for {
		frame, err := stream.Recv()
		if err != nil {
			return err
		}
		if frame.GetStart() != nil {
			p.starts.Add(1)
		}
		if frame.GetBodyEnd() {
			break
		}
	}
	req, err := http.NewRequestWithContext(stream.Context(), http.MethodPost, p.target, strings.NewReader("synthetic"))
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return stream.Send(&pluginv1.ForwardResponse{Frame: &pluginv1.ForwardResponse_Error{Error: &pluginv1.ForwardResponseError{
		Code: "synthetic_wire_control", Message: "local plugin wire control completed", RequestSent: true,
	}}})
}

// The same live manager/runtime route first proves it can dispatch over real
// TCP gRPC and HTTP. Evaluation and linked probes must then stop before either
// wire receives another request, even with RPM unlimited.
func TestR13EvalPluginSocketRejectedBeforeDispatch(t *testing.T) {
	var sends atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		sends.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	plugin := &r13WirePlugin{target: upstream.URL}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	pluginv1.RegisterTransportPluginServer(server, plugin)
	stopped := make(chan struct{})
	go func() { defer close(stopped); _ = server.Serve(listener) }()
	defer func() { server.Stop(); <-stopped }()
	client, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer client.Close()
	manager := &PluginManager{}
	runtime := &pluginRuntime{client: &hcplugin.Client{}, api: pluginv1.NewTransportPluginClient(client)}
	manager.route.Store(&pluginRoute{pluginID: 7, rolloutPercent: 100, runtime: runtime})
	fallback := &hardRPMUpstream{}
	svc, target := evalOAuthHarness(fallback)
	svc.pluginManager = manager
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, upstream.URL, strings.NewReader("synthetic"))
	require.NoError(t, err)
	_, handled, err := manager.RoundTripOpenAIOAuth(ctx, request, "", target.Account)
	require.True(t, handled)
	require.ErrorContains(t, err, "local plugin wire control completed")
	require.EqualValues(t, 1, plugin.starts.Load())
	require.EqualValues(t, 1, sends.Load())
	_, record, err := svc.RunOpenAIEvalSampleAttempts(ctx, target, "OK", "", 3)
	var failure *OpenAIEvalRequestError
	require.ErrorAs(t, err, &failure)
	require.Equal(t, "single_send_unsupported", failure.Code)
	require.False(t, failure.Retryable)
	require.False(t, failure.Attempted)
	require.Zero(t, record.Attempts)
	probe := svc.RunOpenAIStateProbe(ctx, target)
	require.Equal(t, "single_send_unsupported", probe.Failure)
	require.Zero(t, probe.RequestCount)
	require.EqualValues(t, 1, plugin.starts.Load())
	require.EqualValues(t, 1, sends.Load())
	require.Zero(t, fallback.sends)
	require.Zero(t, runtime.inFlight.Load())
}
