package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIStreamTerminatedBeforeOutputFailsOver(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, event := range []string{"response.incomplete", "response.done"} {
			t.Run(fmt.Sprintf("passthrough=%t/%s", passthrough, event), func(t *testing.T) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
				frames := "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_broken\"}}\n\n"
				frames += fmt.Sprintf("event: %s\ndata: {\"type\":%q,\"response\":{\"id\":\"resp_broken\",\"status\":\"incomplete\",\"output\":[],\"incomplete_details\":{\"reason\":\"stream_terminated\"},\"usage\":{\"input_tokens\":7,\"output_tokens\":0}}}\n\n", event, event)
				response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(frames))}
				svc := &OpenAIGatewayService{cfg: &config.Config{}}
				account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
				var err error
				var usage *OpenAIUsage
				if passthrough {
					var result *openaiStreamingResultPassthrough
					result, err = svc.handleStreamingResponsePassthrough(c.Request.Context(), response, c, account, time.Now(), "gpt-6.1-sol", "gpt-6.1-sol")
					require.NotNil(t, result)
					usage = result.usage
				} else {
					var result *openaiStreamingResult
					result, err = svc.handleStreamingResponse(c.Request.Context(), response, c, account, time.Now(), "gpt-6.1-sol", "gpt-6.1-sol")
					require.NotNil(t, result)
					usage = result.usage
				}
				var failover *UpstreamFailoverError
				require.ErrorAs(t, err, &failover)
				require.True(t, failover.SessionAccountEscape)
				require.True(t, failover.RetryableOnSameAccount)
				require.True(t, failover.RetryBeforeSessionEscape)
				require.NotNil(t, usage)
				require.Equal(t, 7, usage.InputTokens)
				require.False(t, c.Writer.Written(), "broken terminal must not be sent before selecting another provider")
			})
		}
	}
}

func TestOpenAIStreamTerminationDoesNotReplayCommittedOrValidOutput(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, tc := range []struct {
			name, prefix, output, reason string
			cancelled                    bool
		}{
			{name: "output_limit", output: "[]", reason: "max_output_tokens"},
			{name: "committed_text", prefix: "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial answer\"}\n\n", output: "[]", reason: "stream_terminated"},
			{name: "terminal_text", output: `[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"partial answer"}]}]`, reason: "stream_terminated"},
			{name: "terminal_tool", output: `[{"type":"custom_tool_call","name":"exec","input":"pwd"}]`, reason: "stream_terminated"},
			{name: "client_cancelled", output: "[]", reason: "stream_terminated", cancelled: true},
		} {
			t.Run(fmt.Sprintf("passthrough=%t/%s", passthrough, tc.name), func(t *testing.T) {
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if tc.cancelled {
					cancel()
				}
				c.Request = httptest.NewRequest("POST", "/v1/responses", nil).WithContext(ctx)
				frames := tc.prefix + fmt.Sprintf("data: {\"type\":\"response.incomplete\",\"response\":{\"id\":\"resp_partial\",\"status\":\"incomplete\",\"output\":%s,\"incomplete_details\":{\"reason\":%q}}}\n\n", tc.output, tc.reason)
				response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(frames))}
				svc := &OpenAIGatewayService{cfg: &config.Config{}}
				account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
				var err error
				if passthrough {
					_, err = svc.handleStreamingResponsePassthrough(ctx, response, c, account, time.Now(), "gpt-6.1-sol", "gpt-6.1-sol")
				} else {
					_, err = svc.handleStreamingResponse(ctx, response, c, account, time.Now(), "gpt-6.1-sol", "gpt-6.1-sol")
				}
				var failover *UpstreamFailoverError
				require.False(t, errors.As(err, &failover), "must not replay after output, on normal truncation, or after client cancellation")
				if !tc.cancelled {
					require.NoError(t, err)
					require.Contains(t, recorder.Body.String(), tc.reason)
				}
			})
		}
	}
}

func TestOpenAIStreamTerminationKeepaliveAccountingAcrossAttempts(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	for attempt := 0; attempt < 3; attempt++ {
		stop := startOpenAISSEKeepalive(c, time.Hour)
		raw, ok := c.Get(openAICompactSSEKeepaliveKey)
		require.True(t, ok)
		keepalive := raw.(*openAICompactSSEKeepalive)
		require.False(t, openAIStreamClientOutputStarted(c, false), "prior attempt heartbeats are not semantic output")
		require.True(t, keepalive.beat())
		require.False(t, openAIStreamClientOutputStarted(c, false))
		stop()
		require.Equal(t, -1, OpenAICompactKeepaliveAdjustedWrittenSize(c))
	}
	_, err := c.Writer.WriteString("data: actual output\n\n")
	require.NoError(t, err)
	require.True(t, openAIStreamClientOutputStarted(c, false))
}
