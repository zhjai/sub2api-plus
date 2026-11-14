package handler

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type bpsCapabilityRouteUpstream struct {
	service.HTTPUpstream
	mu     sync.Mutex
	ids    []int64
	bodies [][]byte
}

func (u *bpsCapabilityRouteUpstream) Do(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	u.mu.Lock()
	u.ids = append(u.ids, id)
	u.bodies = append(u.bodies, body)
	u.mu.Unlock()
	wire := `data: {"type":"response.completed","response":{"id":"resp_capability","status":"completed","model":"gpt-6-astra","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"complete"}]}],"usage":{"input_tokens":1,"output_tokens":1}}}` + "\n\n"
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(wire))}, nil
}

func TestExcelBPSPreSendHostedFallbackUsesQualifiedAccountWithoutBPSRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	bps := service.Account{ID: 881, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1, Credentials: map[string]any{"client_id": openai.ExcelClientID, "model_mapping": map[string]any{"gpt-6-astra": "gpt-6-astra"}}}
	native := service.Account{ID: 882, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 2, Credentials: map[string]any{"api_key": "fixture", "base_url": "https://upstream.example/v1", "model_mapping": map[string]any{"gpt-6-astra": "gpt-6-astra"}}, Extra: map[string]any{"openai_api_key_responses_enabled": true}}
	upstream := new(bpsCapabilityRouteUpstream)
	_, _, _, server, _ := newOpenAIR5Handler(t, []service.Account{bps, native}, testutil.NewRedisGatewayCache(t), upstream)
	defer server.Close()
	status, body := r9PostResponses(t, server, "bps-pre-send", `{"model":"gpt-6-astra","stream":false,"instructions":"retain instruction","input":[{"role":"user","content":"search with full context"}],"tools":[{"type":"web_search"}]}`)
	require.Equal(t, http.StatusOK, status, string(body))
	require.Equal(t, []int64{native.ID}, upstream.ids)
	require.Equal(t, "retain instruction", gjson.GetBytes(upstream.bodies[0], "instructions").String())
	require.Equal(t, "web_search", gjson.GetBytes(upstream.bodies[0], "tools.0.type").String())
}
