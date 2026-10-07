//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptrace"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIEvalBudgetPhysicalSendBoundary(t *testing.T) {
	for _, scenario := range []string{"before_dial", "response", "written", "partial_write", "unknown"} {
		t.Run(scenario, func(t *testing.T) {
			spy := &evalBudgetSpy{}
			c := &openAIEvalController{store: spy, owner: "run", namespace: "fixture"}
			ctx := context.WithValue(t.Context(), openAIEvalControllerKey{}, c)
			req, err := http.NewRequestWithContext(ctx, "POST", "https://invalid.test", nil)
			require.NoError(t, err)
			req, finish := openAIEvalTrackPhysicalSend(req)
			pending := req.Context().Value(openAIEvalPhysicalSendKey{}).(*openAIEvalPhysicalSend)
			pending.prepared.Store(true)
			trace := httptrace.ContextClientTrace(req.Context())
			var response *http.Response
			expected := "uncertain"
			switch scenario {
			case "before_dial":
				trace.GetConn("invalid.test:443")
				expected = "refund"
			case "response":
				response = &http.Response{StatusCode: 200}
				expected = "confirm"
			case "written":
				trace.WroteRequest(httptrace.WroteRequestInfo{})
				expected = "confirm"
			case "partial_write":
				trace.GotConn(httptrace.GotConnInfo{})
				trace.WroteRequest(httptrace.WroteRequestInfo{Err: errors.New("partial write")})
			}
			finish(response, errors.New("synthetic failure"))
			finish(response, nil)
			require.Equal(t, []string{expected}, spy.operations)
			if expected == "confirm" {
				require.Equal(t, 1, openAIEvalRunDiagnostics(ctx).Sent)
			} else {
				require.Zero(t, openAIEvalRunDiagnostics(ctx).Sent)
			}
			if expected == "uncertain" {
				require.Equal(t, "evaluation_send_unknown", openAIEvalRunDiagnostics(ctx).DeferredReason)
			}
		})
	}
}
