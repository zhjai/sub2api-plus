//go:build unit

package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestEvalCodexTurnFinalWire(t *testing.T) {
	for _, mode := range []string{"off", "device", "session", "full"} {
		t.Run(mode, func(t *testing.T) {
			upstream := &evalTransportStub{respond: func(req *http.Request, _ int) (*http.Response, error) {
				body, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				require.Equal(t, chatgptCodexAPIURL, req.URL.String())
				require.Equal(t, "gpt-6-astra", gjson.GetBytes(body, "model").String())
				require.True(t, gjson.GetBytes(body, "stream").Bool())
				require.False(t, gjson.GetBytes(body, "store").Bool())
				require.False(t, gjson.GetBytes(body, "max_output_tokens").Exists())
				require.NotEmpty(t, gjson.GetBytes(body, "instructions").String(), "canonical OAuth defaults still apply")
				require.Equal(t, "auto", gjson.GetBytes(body, "tool_choice").String())
				require.Equal(t, "all_turns", gjson.GetBytes(body, "reasoning.context").String())
				require.Equal(t, "high", gjson.GetBytes(body, "reasoning.effort").String())
				require.Equal(t, "low", gjson.GetBytes(body, "text.verbosity").String())
				input := gjson.GetBytes(body, "input").Array()
				require.Len(t, input, 7)
				require.Equal(t, "additional_tools", input[0].Get("type").String())
				require.True(t, input[0].Get("tools.#(name==\"functions\").tools.#(name==\"exec\")").Exists())
				require.Greater(t, len(input[1].Get("content.0.text").String()), 20000)
				require.Contains(t, input[5].Get("content.0.text").String(), "/home/user/workspace")
				require.Contains(t, input[6].Get("content.0.text").String(), "probe <tag> & answer")
				require.Equal(t, "probe <tag> & answer", input[6].Get("content.0.text").String())
				require.NotContains(t, string(body), `\u003c`)
				require.NotContains(t, string(body), "/home/zhj")
				require.False(t, gjson.GetBytes(body, "previous_response_id").Exists())
				require.Equal(t, "true", req.Header.Get(responsesLiteHeader))
				require.Equal(t, "remote_compaction_v2", req.Header.Get("x-codex-beta-features"))
				require.Equal(t, "en-US,en;q=0.9", req.Header.Get("Accept-Language"))
				require.Empty(t, req.Header.Get("OpenAI-Beta"))
				require.NotEmpty(t, req.Header.Get("Originator"))
				require.NotEmpty(t, req.Header.Get("Version"))
				require.Equal(t, "model=gpt-6-astra", req.Header.Get(openAICodexRoutingHintHeader))
				cm := gjson.GetBytes(body, "client_metadata")
				for header, field := range map[string]string{"session-id": "session_id", "thread-id": "thread_id", "x-client-request-id": "x-client-request-id", "x-codex-installation-id": "x-codex-installation-id", "x-codex-window-id": "x-codex-window-id", openAIWSTurnMetadataHeader: openAIWSTurnMetadataHeader} {
					require.NotEmpty(t, req.Header.Get(header), header)
					require.Equal(t, cm.Get(field).String(), req.Header.Get(header), header)
				}
				require.Equal(t, gjson.GetBytes(body, "prompt_cache_key").String(), req.Header.Get("session_id"))
				turn := cm.Get("turn_id").String()
				embedded := gjson.Parse(req.Header.Get(openAIWSTurnMetadataHeader))
				for field, flat := range map[string]string{"installation_id": "x-codex-installation-id", "session_id": "session_id", "thread_id": "thread_id", "window_id": "x-codex-window-id"} {
					require.Equal(t, cm.Get(flat).String(), embedded.Get(field).String(), field)
				}
				require.Equal(t, turn, cm.Get("root_turn_id").String())
				require.Equal(t, turn, gjson.Get(req.Header.Get(openAIWSTurnMetadataHeader), "turn_id").String())
				require.Equal(t, turn, gjson.Get(req.Header.Get(openAIWSTurnMetadataHeader), "root_turn_id").String())
				for _, item := range input[2:] {
					require.Equal(t, turn, item.Get("internal_chat_message_metadata_passthrough.turn_id").String())
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":" + evalCompletedJSON("21") + "}\n\n"))}, nil
			}}
			svc, target := evalOAuthHarness(upstream)
			target.Credential.Credentials["chatgpt_account_id"] = "wire-test-account"
			target.Credential.Extra = map[string]any{"codex_fingerprint_mode": mode, codexFingerprintSeedExtraKey: testCodexFingerprintSeed}
			response, err := svc.RunOpenAIEvalSample(t.Context(), target, "probe <tag> & answer", "high")
			require.NoError(t, err)
			require.Equal(t, "21", response.Text)
			require.EqualValues(t, 1, upstream.calls.Load())
		})
	}
}

func TestEvalCodexTurnIndependentSessionsAndStableCredential(t *testing.T) {
	now := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	account := &Account{ID: 21, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "same-oauth"}, Extra: map[string]any{"codex_identity_mode": "preserve_client", "codex_identity_api_key_id": float64(7)}}
	first, session, err := newOpenAIEvalCodexPayload(account, "gpt-6-astra", "none", "probe", now)
	require.NoError(t, err)
	prepareOpenAIOAuthDiagnosticPayload(first, account, session)
	duplicate := *account
	duplicate.ID = 27
	second, other, err := newOpenAIEvalCodexPayload(&duplicate, "gpt-6-astra", "", "probe", now)
	require.NoError(t, err)
	prepareOpenAIOAuthDiagnosticPayload(second, &duplicate, other)
	a := first["client_metadata"].(map[string]any)
	b := second["client_metadata"].(map[string]any)
	require.Equal(t, a["x-codex-installation-id"], b["x-codex-installation-id"])
	require.NotEqual(t, a["session_id"], b["session_id"])
	require.NotEqual(t, a["thread_id"], b["thread_id"])
	require.NotEqual(t, a["turn_id"], b["turn_id"])
	require.NotEqual(t, session, a["session_id"], "internal test cannot acquire preserve_client authorization")
	require.Equal(t, "none", first["reasoning"].(map[string]any)["effort"])
	require.NotContains(t, second["reasoning"], "effort")
	otherAccount := *account
	otherAccount.Credentials = map[string]any{"chatgpt_account_id": "other-oauth"}
	third, session, err := newOpenAIEvalCodexPayload(&otherAccount, "gpt-6-astra", "high", "probe", now)
	require.NoError(t, err)
	prepareOpenAIOAuthDiagnosticPayload(third, &otherAccount, session)
	require.NotEqual(t, a["x-codex-installation-id"], third["client_metadata"].(map[string]any)["x-codex-installation-id"])
}

func TestEvalCodexTurnFinalWirePreservesSelectedEffort(t *testing.T) {
	for _, effort := range []string{"", "none", "minimal", "low", "medium", "high", "xhigh", "max"} {
		t.Run("effort="+effort, func(t *testing.T) {
			upstream := &evalTransportStub{respond: func(req *http.Request, _ int) (*http.Response, error) {
				body, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				require.Equal(t, effort, gjson.GetBytes(body, "reasoning.effort").String())
				require.Equal(t, effort != "", gjson.GetBytes(body, "reasoning.effort").Exists())
				metadata := gjson.Parse(req.Header.Get(openAIWSTurnMetadataHeader))
				require.Equal(t, effort, metadata.Get("reasoning_effort").String())
				require.Equal(t, effort != "", metadata.Get("reasoning_effort").Exists())
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":" + evalCompletedJSON("21") + "}\n\n"))}, nil
			}}
			svc, target := evalOAuthHarness(upstream)
			_, err := svc.RunOpenAIEvalSample(t.Context(), target, "probe", effort)
			require.NoError(t, err)
			require.EqualValues(t, 1, upstream.calls.Load())
		})
	}
}

func TestEvalCodexTurnMappedGPT55DoesNotUseLite(t *testing.T) {
	upstream := &evalTransportStub{respond: func(req *http.Request, _ int) (*http.Response, error) {
		require.Empty(t, req.Header.Get(responsesLiteHeader))
		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		require.Equal(t, "gpt-5.5", gjson.GetBytes(body, "model").String())
		require.True(t, gjson.GetBytes(body, "input.0.tools").IsArray())
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":" + evalCompletedJSON("21") + "}\n\n"))}, nil
	}}
	svc, target := evalOAuthHarness(upstream)
	target.UpstreamModel = "gpt-5.5"
	_, err := svc.RunOpenAIEvalSample(t.Context(), target, "probe", "high")
	require.NoError(t, err)
}

func TestEvalCodexContextConcurrentBuildersDoNotMutateFixture(t *testing.T) {
	before := sha256.Sum256(openAIEvalCodexContextJSON)
	require.Equal(t, "65f976b3a7cb410e50312e5b4096686e5b1fda3e4fa1942ee3974d7cb1e7cd42", fmt.Sprintf("%x", before), "pinned public context must not change silently")
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			payload, _, err := newOpenAIEvalCodexPayload(nil, "gpt-6-astra", "high", "probe", time.Now())
			if !assertEvalCodexBuilder(t, payload, err) {
				return
			}
			tools := payload["input"].([]any)[0].(map[string]any)["tools"].(json.RawMessage)
			tools[0] = '!' // Every builder must own its mutable raw bytes.
		})
	}
	wg.Wait()
	require.Equal(t, before, sha256.Sum256(openAIEvalCodexContextJSON))
	payload, _, err := newOpenAIEvalCodexPayload(nil, "gpt-6-astra", "high", "probe", time.Now())
	require.NoError(t, err)
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	require.Equal(t, "functions", gjson.GetBytes(body, "input.0.tools.0.name").String())
	require.Contains(t, openAIEvalCodexContextLicense, "Copyright (c) 2026 Hao Wang")
}

func assertEvalCodexBuilder(t *testing.T, payload map[string]any, err error) bool {
	t.Helper()
	if err != nil || payload == nil {
		t.Errorf("builder failed: %v", err)
		return false
	}
	return true
}

func TestEvalCodexToolCallsRemainDiagnostic(t *testing.T) {
	for _, kind := range []string{"function_call", "custom_tool_call"} {
		for _, location := range []string{"terminal", "added", "done"} {
			t.Run(kind+"/"+location, func(t *testing.T) {
				body := "data: {\"type\":\"response.completed\",\"response\":" + evalCompletedJSON("21") + "}\n\n"
				if location == "terminal" {
					body = fmt.Sprintf("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":%q}],\"usage\":{\"input_tokens\":9,\"output_tokens\":2}}}\n\n", kind)
				} else {
					body = fmt.Sprintf("data: {\"type\":\"response.output_item.%s\",\"item\":{\"type\":%q}}\n\n", location, kind) + body
				}
				upstream := &evalTransportStub{respond: func(*http.Request, int) (*http.Response, error) {
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
				}}
				svc, target := evalOAuthHarness(upstream)
				result, record, err := svc.RunOpenAIEvalSampleAttempts(context.Background(), target, "probe", "high", 3)
				var failure *OpenAIEvalRequestError
				require.ErrorAs(t, err, &failure)
				require.Equal(t, "unexpected_tool_call", failure.Code)
				require.False(t, failure.Retryable)
				require.False(t, record.Valid)
				require.Equal(t, 1, record.Attempts)
				require.Greater(t, result.InputTokens, int64(0))
				require.EqualValues(t, 1, upstream.calls.Load())
			})
		}
	}
}

func TestEvalCodexCompletedItemFallbackStillRequiresTerminal(t *testing.T) {
	body := "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"21\"}]}}\n\n"
	resp := func(body string) *http.Response {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
	}
	_, err := readOpenAIEvalResponse(t.Context(), resp(body), true, true)
	require.Error(t, err)
	result, err := readOpenAIEvalResponse(t.Context(), resp(body+"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":7}}}\n\n"), true, true)
	require.NoError(t, err)
	require.Equal(t, "21", result.Text)
}

func TestEvalCodexBuilderDeveloperInstructions(t *testing.T) {
	now := time.Date(2026, 11, 2, 3, 4, 5, 0, time.UTC)
	plain, _, err := newOpenAIEvalCodexPayload(nil, "gpt-6.1-sol", "none", "question", now)
	require.NoError(t, err)
	payload, session, err := newOpenAIEvalCodexPayloadWithInstructions(nil, "gpt-6.1-sol", "none", "Answer directly.", "question", now)
	require.NoError(t, err)
	body, err := marshalOpenAIEvalPayload(payload)
	require.NoError(t, err)
	plainBody, err := marshalOpenAIEvalPayload(plain)
	require.NoError(t, err)
	input := gjson.GetBytes(body, "input").Array()
	require.Equal(t, "gpt-6.1-sol", gjson.GetBytes(body, "model").String(), "comparison alias must never alter routing")
	require.Equal(t, "Answer directly.", input[2].Get("content.0.text").String())
	require.Equal(t, "generic.developer_instructions", input[2].Get("internal_chat_message_metadata_passthrough.content_item_kinds.0").String())
	require.Equal(t, gjson.GetBytes(plainBody, "input.2.content.0.text").String(), input[2].Get("content.1.text").String())
	require.Equal(t, "question", input[len(input)-1].Get("content.0.text").String())
	metadata := gjson.Parse(gjson.GetBytes(body, "client_metadata").Get(openAIWSTurnMetadataHeader).String())
	require.Equal(t, "new", metadata.Get("history_initialization").String())
	require.Equal(t, now.UnixMilli(), metadata.Get("turn_started_at_unix_ms").Int())
	require.Equal(t, session, gjson.GetBytes(body, "prompt_cache_key").String())
	require.Equal(t, "none", metadata.Get("reasoning_effort").String())
}
