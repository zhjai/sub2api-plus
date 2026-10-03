package service

import (
	"context"
	"errors"
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

func opaqueModelMismatchSSEBody() string {
	return strings.Join([]string{
		`event: response.created`,
		`data: {"type":"response.created","response":{"id":"resp_wrong_sse","model":"gpt-4.1-mini","status":"in_progress"}}`,
		``,
		`event: response.completed`,
		`data: {"type":"response.completed","response":{"id":"resp_wrong_sse","model":"gpt-4.1-mini","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`,
		``,
	}, "\n")
}

func opaqueModelMismatchSSEResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(opaqueModelMismatchSSEBody())),
	}
}

func opaqueModelMismatchTestAccount() *Account {
	return &Account{
		ID:       7401,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Extra:    map[string]any{"openai_opaque_upstream": true},
	}
}

func TestHandleNonStreamingResponsePassthrough_OpaqueModelMismatchBeforeCommit(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name        string
		contentType string
		body        string
	}{
		{
			name:        "json",
			contentType: "application/json",
			body:        `{"id":"resp_wrong_json","model":"gpt-4.1-mini","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`,
		},
		{
			name:        "sse",
			contentType: "text/event-stream",
			body:        opaqueModelMismatchSSEBody(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{tt.contentType}},
				Body:       io.NopCloser(strings.NewReader(tt.body)),
			}
			account := opaqueModelMismatchTestAccount()

			result, err := (&OpenAIGatewayService{cfg: &config.Config{}}).handleNonStreamingResponsePassthrough(
				context.Background(), resp, c, account, "gpt-5.5", "gpt-5.5",
			)
			require.Nil(t, result)
			var failoverErr *UpstreamFailoverError
			require.ErrorAs(t, err, &failoverErr)
			require.True(t, failoverErr.OpaqueRouteEpochEligible)
			require.Equal(t, OpenAIIntegritySignalModelMismatch, failoverErr.IntegritySignal)
			require.Equal(t, "gpt-5.5", failoverErr.IntegritySentModel)
			require.Equal(t, "gpt-4.1-mini", failoverErr.IntegrityResponseModel)
			require.False(t, c.Writer.Written(), "mismatch must be rejected before the downstream 200 response")
			require.Empty(t, rec.Body.String())
			require.NotContains(t, string(failoverErr.ResponseBody), "resp_wrong")
		})
	}
}

func TestHTTPStreaming_OpaqueModelMismatchBeforeCommit(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, passthrough := range []bool{false, true} {
		name := "native"
		if passthrough {
			name = "passthrough"
		}
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			svc := &OpenAIGatewayService{cfg: &config.Config{}}
			var err error
			if passthrough {
				_, err = svc.handleStreamingResponsePassthrough(context.Background(), opaqueModelMismatchSSEResponse(), c, opaqueModelMismatchTestAccount(), time.Now(), "gpt-5.5", "gpt-5.5")
			} else {
				_, err = svc.handleStreamingResponse(context.Background(), opaqueModelMismatchSSEResponse(), c, opaqueModelMismatchTestAccount(), time.Now(), "gpt-5.5", "gpt-5.5")
			}
			var failoverErr *UpstreamFailoverError
			require.ErrorAs(t, err, &failoverErr)
			require.True(t, failoverErr.OpaqueRouteEpochEligible)
			require.Equal(t, OpenAIIntegritySignalModelMismatch, failoverErr.IntegritySignal)
			require.False(t, c.Writer.Written())
			require.Empty(t, rec.Body.String())
		})
	}
}

func TestHandleNonStreamingResponse_OpaqueSSEModelMismatchBeforeCommit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	result, err := (&OpenAIGatewayService{cfg: &config.Config{}}).handleNonStreamingResponse(
		context.Background(), opaqueModelMismatchSSEResponse(), c, opaqueModelMismatchTestAccount(), "gpt-5.5", "gpt-5.5",
	)
	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.OpaqueRouteEpochEligible)
	require.Equal(t, OpenAIIntegritySignalModelMismatch, failoverErr.IntegritySignal)
	require.False(t, c.Writer.Written())
	require.Empty(t, rec.Body.String())
}

func TestForwardOpenAIWSV2_OpaqueModelMismatchBeforeSemanticWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name   string
		stream bool
	}{
		{name: "stream", stream: true},
		{name: "nonstream", stream: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

			cfg := newOpenAIWSV2TestConfig()
			cfg.Security.URLAllowlist.Enabled = false
			cfg.Security.URLAllowlist.AllowInsecureHTTP = true
			cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
			captureConn := &openAIWSCaptureConn{events: [][]byte{
				[]byte(`{"type":"response.created","response":{"id":"resp_wrong_ws","model":"gpt-4.1-mini","status":"in_progress"}}`),
				[]byte(`{"type":"response.completed","response":{"id":"resp_wrong_ws","model":"gpt-4.1-mini","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`),
			}}
			pool := newOpenAIWSConnPool(cfg)
			pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: captureConn})
			svc := &OpenAIGatewayService{
				cfg:              cfg,
				httpUpstream:     &httpUpstreamRecorder{},
				cache:            &stubGatewayCache{},
				openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
				toolCorrector:    NewCodexToolCorrector(),
				openaiWSPool:     pool,
			}
			account := &Account{
				ID:          7402,
				Name:        "opaque-ws-model-mismatch",
				Platform:    PlatformOpenAI,
				Type:        AccountTypeAPIKey,
				Status:      StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Credentials: map[string]any{"api_key": "sk-test"},
				Extra: map[string]any{
					"responses_websockets_v2_enabled": true,
					"openai_opaque_upstream":          true,
				},
			}
			body := []byte(`{"model":"gpt-5.5","input":"hello"}`)
			if tt.stream {
				body = []byte(`{"model":"gpt-5.5","stream":true,"input":"hello"}`)
			}

			result, err := svc.Forward(context.Background(), c, account, body)
			require.Nil(t, result)
			var failoverErr *UpstreamFailoverError
			require.True(t, errors.As(err, &failoverErr), "expected epoch-retry failover, got %T: %v", err, err)
			require.True(t, failoverErr.OpaqueRouteEpochEligible)
			require.Equal(t, OpenAIIntegritySignalModelMismatch, failoverErr.IntegritySignal)
			require.Equal(t, "gpt-5.5", failoverErr.IntegritySentModel)
			require.Equal(t, "gpt-4.1-mini", failoverErr.IntegrityResponseModel)
			require.False(t, c.Writer.Written(), "response.created must stay hidden from the downstream client")
			require.Empty(t, rec.Body.String())
			require.NotContains(t, string(failoverErr.ResponseBody), "resp_wrong_ws")
		})
	}
}
