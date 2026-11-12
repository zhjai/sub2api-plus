package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func excelBPSAccount(t *testing.T) *Account {
	return &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 2,
		Credentials: map[string]any{"client_id": openai.ExcelClientID, "access_token": excelTestJWT(t, nil), "chatgpt_account_id": "workspace-test", "plan_type": "plus"}}
}

const excelBPSSuccessWire = "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_bps\",\"output\":[]}}\n\n" +
	"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n" +
	"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_bps\",\"status\":\"completed\",\"model\":\"gpt-6-astra\",\"output\":[{\"type\":\"message\",\"id\":\"msg_bps\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello\",\"annotations\":[]}]}],\"usage\":{\"input_tokens\":7,\"output_tokens\":3}}}\n\n"

func excelBPSMockResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"text/event-stream"}, "X-Request-Id": {"bps-request"}}, Body: io.NopCloser(strings.NewReader(body))}
}

type excelBPSSingleSendRecorder struct{ *httpUpstreamRecorder }

func (*excelBPSSingleSendRecorder) SupportsSingleSend() bool { return true }

func TestOpenAIExcelBPSForwardStreamingAndJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{true: "stream", false: "json"}[stream], func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: excelBPSMockResponse(200, excelBPSSuccessWire)}
			s := openAIClientToolsTestService(upstream)
			a := excelBPSAccount(t)
			a.Credentials["model_mapping"] = map[string]any{"client-model": "gpt-6-astra"}
			body := []byte(`{"model":"client-model","input":"hello","reasoning":{"effort":"max"},"client_metadata":{"thread_id":"local-private-thread","os":"local-private-os"},"prompt_cache_key":"local-private-cache","stream":` + map[bool]string{true: "true", false: "false"}[stream] + "}")
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(body))
			c.Request.Header.Set("Cookie", "private-cookie")
			c.Request.Header.Set("X-Codex-Installation-Id", "private-installation")
			r, err := s.Forward(context.Background(), c, a, body)
			require.NoError(t, err)
			require.NotNil(t, r)
			require.Equal(t, basispoints.ResponsesURL, upstream.lastReq.URL.String())
			require.True(t, HTTPUpstreamRedirectsDisabled(upstream.lastReq.Context()))
			require.Equal(t, "client-model", r.Model)
			require.Equal(t, "gpt-6-astra", r.BillingModel)
			require.Equal(t, "gpt-6-astra", r.UpstreamModel)
			require.Equal(t, "/basispoints/api/responses", r.UpstreamEndpoint)
			require.Equal(t, 7, r.Usage.InputTokens)
			require.Equal(t, 3, r.Usage.OutputTokens)
			require.Equal(t, "xhigh", *r.ReasoningEffort)
			require.Contains(t, rec.Body.String(), "hello")
			require.Equal(t, "gpt-6-astra", gjson.GetBytes(upstream.lastBody, "model").String())
			for _, value := range []string{"private-cookie", "private-installation", "local-private-thread", "local-private-os", "local-private-cache"} {
				require.NotContains(t, string(upstream.lastBody), value)
			}
			for _, header := range []string{"Cookie", "Session-Id", "Originator", "X-Codex-Installation-Id", "X-Codex-Turn-Metadata"} {
				require.Empty(t, upstream.lastReq.Header.Get(header), header)
			}
			require.Equal(t, "workspace-test", upstream.lastReq.Header.Get("Chatgpt-Account-Id"))
			require.Equal(t, "en-US,en;q=0.9", upstream.lastReq.Header.Get("Accept-Language"))
			require.Equal(t, 1, len(upstream.requests))
		})
	}
}

func TestOpenAIExcelBPSUnsupportedRequestsNeverReachCodex(t *testing.T) {
	s := openAIClientToolsTestService(&httpUpstreamRecorder{})
	a := excelBPSAccount(t)
	require.True(t, a.IsExcelOAuth())
	require.False(t, a.UsesOpenAICodexProtocol())
	require.True(t, a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityResponses))
	for _, capability := range []OpenAIEndpointCapability{OpenAIEndpointCapabilityChatCompletions, OpenAIEndpointCapabilityEmbeddings, OpenAIEndpointCapabilityAlphaSearch, OpenAIEndpointCapabilityLive} {
		require.False(t, a.SupportsOpenAIEndpointCapability(capability))
	}
	require.Zero(t, openAICompactSupportTier(a))
	require.Equal(t, "excel_bps_http_only", NewOpenAIWSProtocolResolver(s.cfg).Resolve(a).Reason)
	for _, path := range []string{"/v1/responses/compact", "/v1/responses"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", path, nil)
		_, err := s.Forward(context.Background(), c, a, []byte(`{"model":"gpt-6-astra","input":"hi","previous_response_id":"resp_old"}`))
		require.Error(t, err)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	_, err := s.ForwardAsChatCompletions(context.Background(), c, a, nil, "", "")
	require.Error(t, err)
}

func TestOpenAIExcelBPSExcludedFromWebSocketScheduling(t *testing.T) {
	a := excelBPSAccount(t)
	for _, enabled := range []bool{false, true} {
		s := &OpenAIGatewayService{cfg: &config.Config{}}
		s.cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = enabled
		for _, mode := range []string{OpenAIWSIngressModeHTTPBridge, OpenAIWSIngressModeCtxPool, OpenAIWSIngressModePassthrough} {
			a.Extra = map[string]any{"openai_oauth_responses_websockets_v2_mode": mode}
			require.True(t, s.isOpenAIAccountTransportCompatible(a, OpenAIUpstreamTransportHTTPSSE))
			require.False(t, s.isOpenAIAccountTransportCompatible(a, OpenAIUpstreamTransportResponsesWebsocketV2Ingress))
			require.False(t, s.isOpenAIAccountTransportCompatible(a, OpenAIUpstreamTransportResponsesWebsocketV2))
		}
	}
}

func TestOpenAIExcelBPSAccountTestUsesSameBridge(t *testing.T) {
	a := excelBPSAccount(t)
	upstream := &httpUpstreamRecorder{resp: excelBPSMockResponse(200, excelBPSSuccessWire)}
	s := &AccountTestService{httpUpstream: upstream}
	req := httptest.NewRequest("POST", chatgptCodexAPIURL, strings.NewReader(`{"model":"gpt-6-astra","input":"hello","stream":true}`))
	req.Header.Set("Authorization", "Bearer "+a.GetOpenAIAccessToken())
	req.Header.Set("X-Codex-Installation-Id", "private-installation")
	resp, err := s.doOpenAIAccountTestUpstream(req, "", a, false)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, basispoints.ResponsesURL, upstream.lastReq.URL.String())
	require.Empty(t, upstream.lastReq.Header.Get("X-Codex-Installation-Id"))
	require.Contains(t, string(raw), "response.completed")
}

func TestOpenAIExcelBPSHTTPErrorDoesNotEchoGrant(t *testing.T) {
	a := excelBPSAccount(t)
	upstream := &httpUpstreamRecorder{resp: excelBPSMockResponse(403, `{"error":{"code":"permission_error","message":"denied `+a.GetOpenAIAccessToken()+`"}}`)}
	s := openAIClientToolsTestService(upstream)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/responses", nil)
	_, err := s.Forward(context.Background(), c, a, []byte(`{"model":"gpt-6-astra","input":"hi"}`))
	require.Error(t, err)
	require.NotContains(t, rec.Body.String(), a.GetOpenAIAccessToken())
}

func TestOpenAIExcelBPSConnectionErrorDoesNotEchoGrant(t *testing.T) {
	a := excelBPSAccount(t)
	a.Credentials["refresh_token"] = "excel-private-refresh"
	a.Credentials["id_token"] = "excel-private-id"
	body := `{"error":{"code":"permission_error","message":"denied ` + a.GetOpenAIAccessToken() + ` excel-private-refresh excel-private-id"}}`
	upstream := &httpUpstreamRecorder{resp: excelBPSMockResponse(403, body)}
	s := &AccountTestService{httpUpstream: upstream}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/admin/accounts/42/test", nil)
	err := s.testOpenAIAccountConnection(c, a, "gpt-6-astra", "", "")
	require.Error(t, err)
	for _, secret := range []string{a.GetOpenAIAccessToken(), "excel-private-refresh", "excel-private-id"} {
		require.NotContains(t, rec.Body.String(), secret)
		require.NotContains(t, err.Error(), secret)
	}
	require.Contains(t, rec.Body.String(), "permission_error")
}

func TestOpenAIExcelBPSEvaluationUsesSameBridge(t *testing.T) {
	a := excelBPSAccount(t)
	upstream := &excelBPSSingleSendRecorder{&httpUpstreamRecorder{resp: excelBPSMockResponse(200, excelBPSSuccessWire)}}
	s := &AccountTestService{httpUpstream: upstream}
	req := httptest.NewRequest("POST", chatgptCodexAPIURL, strings.NewReader(`{"model":"gpt-6-astra","input":"evaluation","stream":true}`))
	req.Header.Set("Authorization", "Bearer "+a.GetOpenAIAccessToken())
	req.Header.Set("X-Codex-Installation-Id", "private-installation")
	resp, err := s.doOpenAIEvalUpstream(req, "", a, a)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, basispoints.ResponsesURL, upstream.lastReq.URL.String())
	require.True(t, HTTPUpstreamSingleSendRequired(upstream.lastReq.Context()))
	require.Empty(t, upstream.lastReq.Header.Get("X-Codex-Installation-Id"))
	require.Contains(t, string(raw), "response.completed")
}

func TestOpenAIExcelBPSDoesNotSupportNativeTicketProbe(t *testing.T) {
	a := excelBPSAccount(t)
	require.False(t, isOpenAIStateProbeTarget(&OpenAIEvalTarget{Account: a, Credential: a}))
}

func TestOpenAIExcelBPSModelsManifestPreservesPlaintextV2(t *testing.T) {
	a := excelBPSAccount(t)
	a.Credentials["model_mapping"] = map[string]any{"bps-alias": "gpt-6-astra"}
	body := []byte(`{"models":[{"slug":"bps-alias","multi_agent_version":"v2","multi_agent_reasoning_effort":"xhigh"},{"slug":"native-alias","multi_agent_version":"v2"}]}`)
	out, changed, err := restrictExcelBPSModels(body, PlatformOpenAI, []Account{*a}, nil, nil, true)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "v2", gjson.GetBytes(out, "models.0.multi_agent_version").String())
	require.Equal(t, gjson.Null, gjson.GetBytes(out, "models.0.multi_agent_reasoning_effort").Type)
	require.Equal(t, "v2", gjson.GetBytes(out, "models.1.multi_agent_version").String())
	native := *a
	native.Credentials = map[string]any{"client_id": openai.ClientID}
	out, changed, err = restrictExcelBPSModels(body, PlatformOpenAI, []Account{native}, nil, nil, true)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, out)
	generated, err := buildCodexModelsManifestForAccounts(PlatformOpenAI, []string{"bps-alias"}, []Account{*a}, nil, nil, true)
	require.NoError(t, err)
	require.Equal(t, "v2", gjson.GetBytes(generated, "models.0.multi_agent_version").String())
	require.Equal(t, gjson.Null, gjson.GetBytes(generated, "models.0.multi_agent_reasoning_effort").Type)
}

func TestOpenAIExcelBPSV2DoesNotUpgradeRestrictedUpstream(t *testing.T) {
	bps := excelBPSAccount(t)
	unknown := Account{ID: 43, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://relay.example/v1", "api_key": "synthetic"}}
	body, err := buildCodexModelsManifestForAccounts(PlatformOpenAI,
		[]string{"gpt-6-astra"}, []Account{*bps, unknown}, nil, nil, true)
	require.NoError(t, err)
	require.Equal(t, gjson.Null, gjson.GetBytes(body, "models.0.multi_agent_version").Type)

	// Explicit restrictions from selected live/pinned sources remain valid.
	restricted := []byte("{\"models\":[{\"slug\":\"gpt-6-astra\",\"multi_agent_version\":null,\"multi_agent_reasoning_effort\":null}]}")
	out, changed, err := restrictExcelBPSModels(restricted, PlatformOpenAI,
		[]Account{*bps}, nil, nil, true)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, restricted, out)
	native := Account{ID: 44, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"client_id": openai.ClientID}}
	v2 := []byte("{\"models\":[{\"slug\":\"gpt-6-astra\",\"multi_agent_version\":\"v2\",\"multi_agent_reasoning_effort\":null}]}")
	out, changed, err = restrictExcelBPSModels(v2, PlatformOpenAI,
		[]Account{*bps, native}, nil, nil, true)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, gjson.Null, gjson.GetBytes(out, "models.0.multi_agent_version").Type)

	// Explicit routing selects the owned adapter's verified plaintext v2
	// support rather than unrelated accounts without capability evidence.
	bps.Credentials["model_mapping"] = map[string]any{"bps-alias": "gpt-6-astra"}
	group := &Group{ModelRoutingEnabled: true, ModelRouting: map[string][]int64{"bps-alias": {bps.ID}}}
	body, err = buildCodexModelsManifestForAccounts(PlatformOpenAI,
		[]string{"bps-alias"}, []Account{*bps, unknown}, group, nil, true)
	require.NoError(t, err)
	require.Equal(t, "v2", gjson.GetBytes(body, "models.0.multi_agent_version").String())
}

func TestOpenAIExcelBPSGatewayToolContinuation(t *testing.T) {
	for _, kind := range []string{"function", "custom"} {
		t.Run(kind, func(t *testing.T) {
			encode := func(value any) []byte { raw, err := json.Marshal(value); require.NoError(t, err); return raw }
			envelope := map[string]any{"name": "client.run", "arguments": map[string]any{"number": 21}}
			if kind == "custom" {
				delete(envelope, "arguments")
				envelope["input"] = "exact raw input\n"
			}
			args := encode(map[string]any{"summary": "Call client tool", "code": string(encode(envelope)), "destructive": false, "references": []any{}})
			native := map[string]any{"type": "function_call", "name": "run_officejs", "id": "fc_bps", "call_id": "call_bps", "arguments": string(args), "status": "completed"}
			wire := "data: " + string(encode(map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_tool", "status": "completed", "output": []any{native}}})) + "\n\n"
			upstream := &httpUpstreamRecorder{resp: excelBPSMockResponse(200, wire)}
			s := openAIClientToolsTestService(upstream)
			a := excelBPSAccount(t)
			body := map[string]any{"model": "gpt-6-astra", "stream": false, "input": "hello", "tools": []any{map[string]any{"type": "namespace", "name": "client", "tools": []any{map[string]any{"type": kind, "name": "run"}}}}}
			forward := func(raw []byte) (*httptest.ResponseRecorder, error) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(raw))
				c.Request.Header.Set("Session-Id", t.Name())
				_, err := s.Forward(context.Background(), c, a, raw)
				return rec, err
			}
			rec, err := forward(encode(body))
			require.NoError(t, err)
			call := gjson.Get(rec.Body.String(), "output.0")
			callType := "function_call"
			if kind == "custom" {
				callType = "custom_tool_call"
			}
			require.Equal(t, callType, call.Get("type").String())
			require.Equal(t, "run", call.Get("name").String())
			require.NotContains(t, rec.Body.String(), "run_officejs")
			var clientCall map[string]any
			require.NoError(t, json.Unmarshal([]byte(call.Raw), &clientCall))
			body["input"] = []any{map[string]any{"role": "user", "content": "hello"}, clientCall, map[string]any{"type": callType + "_output", "call_id": "call_bps", "output": "done"}}
			upstream.resp = excelBPSMockResponse(200, excelBPSSuccessWire)
			rec, err = forward(encode(body))
			require.NoError(t, err)
			require.Contains(t, rec.Body.String(), "hello")
			require.Contains(t, string(upstream.lastBody), "run_officejs")
			require.Contains(t, string(upstream.lastBody), "call_bps")
			require.Len(t, upstream.requests, 2)
		})
	}
}

func TestOpenAIExcelBPSGatewayKeepsPlaintextCollaborationMetadata(t *testing.T) {
	for _, operation := range []string{"spawn_agent", "send_message", "followup_task"} {
		for _, stream := range []bool{false, true} {
			t.Run(operation+map[bool]string{false: "/json", true: "/sse"}[stream], func(t *testing.T) {
				encode := func(value any) []byte { raw, err := json.Marshal(value); require.NoError(t, err); return raw }
				const task = "Synthetic handoff: preserve the exact task.\nCheck value 21."
				arguments := map[string]any{"message": task, "target": "worker"}
				if operation == "spawn_agent" {
					delete(arguments, "target")
					arguments["task_name"] = "worker"
				}
				envelope := map[string]any{"name": "collaboration." + operation, "arguments": arguments}
				args := encode(map[string]any{"summary": "Deliver synthetic task", "code": string(encode(envelope)), "destructive": false, "references": []any{}})
				native := map[string]any{"type": "function_call", "name": "run_officejs", "id": "fc_agent", "call_id": "call_agent", "arguments": string(args), "status": "completed"}
				wire := "data: " + string(encode(map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_agent", "output": []any{}}})) + "\n\n" +
					"data: " + string(encode(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": native})) + "\n\n" +
					"data: " + string(encode(map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_agent", "status": "completed", "output": []any{native}}})) + "\n\n"
				upstream := &httpUpstreamRecorder{resp: excelBPSMockResponse(200, wire)}
				s := openAIClientToolsTestService(upstream)
				a := excelBPSAccount(t)
				body := map[string]any{"model": "gpt-6-astra", "stream": stream, "input": "Delegate the synthetic task", "tools": []any{map[string]any{
					"type": "namespace", "name": "collaboration", "tools": []any{map[string]any{"type": "function", "name": operation, "parameters": map[string]any{
						"type": "object", "properties": map[string]any{"message": map[string]any{"type": "string", "encrypted": true}},
					}}},
				}}}
				forward := func(input map[string]any) *httptest.ResponseRecorder {
					raw := encode(input)
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					c.Request = httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(raw))
					_, err := s.Forward(context.Background(), c, a, raw)
					require.NoError(t, err)
					return rec
				}
				rec := forward(body)
				var call gjson.Result
				if stream {
					marked := 0
					for _, line := range strings.Split(rec.Body.String(), "\n") {
						if !strings.HasPrefix(line, "data: ") {
							continue
						}
						event := gjson.Parse(strings.TrimPrefix(line, "data: "))
						switch event.Get("type").String() {
						case "response.output_item.added", "response.output_item.done":
							require.Equal(t, "[]", event.Get("item.encrypted_function_args").Raw)
							marked++
						case "response.completed":
							call = event.Get("response.output.0")
						}
					}
					require.Equal(t, 2, marked)
				} else {
					call = gjson.Get(rec.Body.String(), "output.0")
				}
				require.Equal(t, "collaboration", call.Get("namespace").String())
				require.Equal(t, operation, call.Get("name").String())
				require.Equal(t, "[]", call.Get("encrypted_function_args").Raw)
				require.Equal(t, task, gjson.Parse(call.Get("arguments").String()).Get("message").String())
				// A BPS child must receive the original plaintext, not ciphertext or an omission marker.
				body["input"] = []any{map[string]any{"type": "agent_message", "author": "/root", "recipient": "/root/worker", "content": []any{map[string]any{"type": "input_text", "text": task}}}}
				upstream.resp = excelBPSMockResponse(200, excelBPSSuccessWire)
				forward(body)
				found := false
				gjson.GetBytes(upstream.lastBody, "input").ForEach(func(_, item gjson.Result) bool {
					item.Get("content").ForEach(func(_, part gjson.Result) bool { found = found || part.Get("text").String() == task; return true })
					return true
				})
				require.True(t, found, "the complete task must reach the child unchanged")
			})
		}
	}
}

func TestExcelBPSConversationScopesSeparateSiblingThreads(t *testing.T) {
	contextFor := func(thread, metadata string) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
		c.Request.Header.Set("Session-Id", "shared-parent-session")
		c.Request.Header.Set("Thread-Id", thread)
		c.Request.Header.Set("X-Codex-Turn-Metadata", metadata)
		return c
	}
	parent, ok := excelBPSConversationScope(contextFor("parent", ""), nil, 42)
	require.True(t, ok)
	child, ok := excelBPSConversationScope(contextFor("child", ""), nil, 42)
	require.True(t, ok)
	require.NotEqual(t, parent, child)
	next, _ := excelBPSConversationScope(contextFor("child", ""), []byte(`{"input":"next turn"}`), 42)
	require.Equal(t, child, next)
	metadata, _ := excelBPSConversationScope(contextFor("", `{"thread_id":"child"}`), nil, 42)
	require.Equal(t, child, metadata)
	body, _ := excelBPSConversationScope(contextFor("", ""), []byte(`{"client_metadata":{"thread_id":"child"}}`), 42)
	require.Equal(t, child, body)
	other, _ := excelBPSConversationScope(contextFor("child", ""), nil, 43)
	require.NotEqual(t, child, other)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	anonymous, cache := excelBPSConversationScope(c, []byte(`{"input":"same text"}`), 42)
	require.False(t, cache)
	require.Empty(t, anonymous)
}

// This transport exercises both gateway adapters without credentials/network.
// It is protocol integration coverage, not a real Codex runtime acceptance test.
type excelBPSTwoHopTransport struct {
	*httpUpstreamRecorder
	inner   *OpenAIGatewayService
	account *Account
}

func (u *excelBPSTwoHopTransport) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req.Clone(req.Context())
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	_, forwardErr := u.inner.Forward(req.Context(), c, u.account, body)
	if forwardErr != nil && !c.Writer.Written() {
		return nil, fmt.Errorf("synthetic inner gateway: %w", forwardErr)
	}
	return rec.Result(), nil
}

func TestOpenAIExcelBPSTwoHopCollaboration(t *testing.T) {
	for _, stream := range []bool{true, false} {
		t.Run(map[bool]string{true: "sse", false: "json"}[stream], func(t *testing.T) {
			encode := func(v any) string { b, err := json.Marshal(v); require.NoError(t, err); return string(b) }
			bps := &httpUpstreamRecorder{}
			inner := openAIClientToolsTestService(bps)
			outer := openAIClientToolsTestService(nil)
			outer.httpUpstream = &excelBPSTwoHopTransport{httpUpstreamRecorder: new(httpUpstreamRecorder), inner: inner, account: excelBPSAccount(t)}
			proxy := &Account{ID: 43, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic", "base_url": "https://relay.example/v1"}}
			forward := func(thread string, input any, tools []any) gjson.Result {
				raw := []byte(encode(map[string]any{"model": "gpt-6-astra", "stream": stream, "input": input, "tools": tools}))
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(raw))
				c.Request.Header.Set("Thread-Id", thread)
				c.Request.Header.Set("Session-Id", "shared-session")
				_, err := outer.Forward(context.Background(), c, proxy, raw)
				require.NoError(t, err, fmt.Sprintf("outer body: %s; inner upstream calls: %d", rec.Body.String(), len(bps.requests)))
				if !stream {
					return gjson.Parse(rec.Body.String())
				}
				var completed gjson.Result
				for _, line := range strings.Split(rec.Body.String(), "\n") {
					if strings.HasPrefix(line, "data: ") {
						event := gjson.Parse(strings.TrimPrefix(line, "data: "))
						if event.Get("type").String() == "response.completed" {
							completed = event.Get("response")
						}
					}
				}
				require.True(t, completed.Exists(), rec.Body.String())
				return completed
			}
			for _, operation := range []string{"spawn_agent", "send_message", "followup_task"} {
				const task = "Synthetic task: preserve quotes \" and Unicode 核验."
				tools := []any{map[string]any{"type": "namespace", "name": "collaboration", "tools": []any{map[string]any{"type": "function", "name": operation, "parameters": map[string]any{"type": "object", "properties": map[string]any{"message": map[string]any{"type": "string", "encrypted": true}}}}}}}
				envelope := map[string]any{"name": "collaboration." + operation, "arguments": map[string]any{"message": task}}
				native := map[string]any{"type": "function_call", "name": "run_officejs", "call_id": "call_" + operation, "id": "fc_" + operation, "arguments": encode(map[string]any{"code": encode(envelope), "summary": "Delegate", "destructive": false, "references": []any{}}), "status": "completed"}
				response := map[string]any{"id": "resp_parent", "status": "completed", "output": []any{native}}
				bps.resp = excelBPSMockResponse(200, "data: "+encode(map[string]any{"type": "response.completed", "response": response})+"\n\n")
				result := forward("parent", "Delegate", tools)
				call := result.Get("output.0")
				require.Equal(t, "collaboration", call.Get("namespace").String())
				require.Equal(t, operation, call.Get("name").String())
				require.Equal(t, "[]", call.Get("encrypted_function_args").Raw)
				require.Equal(t, task, gjson.Parse(call.Get("arguments").String()).Get("message").String())
				bps.resp = excelBPSMockResponse(200, excelBPSSuccessWire)
				forward("child", []any{map[string]any{"type": "agent_message", "author": "/root", "recipient": "/root/worker", "content": []any{map[string]any{"type": "input_text", "text": task}}}}, nil)
				require.Contains(t, string(bps.lastBody), "核验")
				bps.resp = excelBPSMockResponse(200, excelBPSSuccessWire)
				forward("parent", []any{json.RawMessage(call.Raw), map[string]any{"type": "function_call_output", "call_id": call.Get("call_id").String(), "output": "delivered"}, map[string]any{"type": "agent_message", "author": "/root/worker", "recipient": "/root", "content": []any{map[string]any{"type": "input_text", "text": "Synthetic child result"}}}}, tools)
				require.Contains(t, string(bps.lastBody), "Synthetic child result")
			}
		})
	}
}

func TestExcelBPSCurrentAssignmentErrorHasSafeParam(t *testing.T) {
	upstream := new(httpUpstreamRecorder)
	s := openAIClientToolsTestService(upstream)
	body := []byte(`{"model":"gpt-6-astra","input":[{"type":"agent_message","content":[{"type":"input_text","text":"Readable routing header"},{"type":"encrypted_content","data":"SYNTHETIC_PRIVATE_ASSIGNMENT"}]}]}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(body))
	_, err := s.Forward(context.Background(), c, excelBPSAccount(t), body)
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "input[0].content[1]", gjson.Get(rec.Body.String(), "error.param").String())
	require.NotContains(t, rec.Body.String(), "SYNTHETIC_PRIVATE_ASSIGNMENT")
	require.Empty(t, upstream.requests)
}

func TestExcelBPSForwardPreservesAccountErrorReason(t *testing.T) {
	upstream := new(httpUpstreamRecorder)
	s := openAIClientToolsTestService(upstream)
	a := excelBPSAccount(t)
	a.Credentials["plan_type"] = "free"
	body := []byte(`{"model":"gpt-6-astra","input":"hello"}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(body))
	_, err := s.Forward(context.Background(), c, a, body)
	require.Error(t, err)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "OPENAI_EXCEL_OAUTH_PLAN_UNSUPPORTED", gjson.Get(rec.Body.String(), "error.code").String())
	require.Empty(t, upstream.requests)
}
