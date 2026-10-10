//go:build unit

package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func evalChatJSON(answer, reason string) string {
	text, _ := json.Marshal(answer)
	return fmt.Sprintf(`{"model":"gpt-6.1-sol","choices":[{"index":0,"message":{"role":"assistant","content":%s},"finish_reason":%q}],"usage":{"prompt_tokens":7,"completion_tokens":2}}`, text, reason)
}

func TestEvalChatResponseCompletion(t *testing.T) {
	for _, tc := range []struct {
		name, body, answer, code string
	}{
		{name: "JSON", body: evalChatJSON("21", "stop"), answer: "21"},
		{name: "content parts", body: `{"choices":[{"message":{"role":"assistant","content":[{"type":"text","text":"2"},{"type":"text","text":"1"}]},"finish_reason":"stop"}]}`, answer: "21"},
		{name: "SSE with usage", body: "data: {\"model\":\"gpt-6.1-sol\",\"choices\":[{\"delta\":{\"content\":\"21\"},\"finish_reason\":null}]}\r\n\r\n" +
			"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\r\n\r\n" +
			"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":2}}\r\n\r\n" +
			"data: [DONE]", answer: "21"},
		{name: "truncated", body: evalChatJSON("21", "length"), code: "max_output_tokens"},
		{name: "filtered", body: evalChatJSON("21", "content_filter"), code: "content_filter"},
		{name: "missing finish", body: evalChatJSON("21", ""), code: "missing_terminal"},
		{name: "premature SSE EOF", body: "data: {\"choices\":[{\"delta\":{\"content\":\"21\"},\"finish_reason\":\"stop\"}]}\n\n", code: "missing_terminal"},
		{name: "DONE alone", body: "data: [DONE]\n\n", code: "missing_terminal"},
		{name: "reasoning only", body: `{"choices":[{"message":{"role":"assistant","reasoning_content":"21"},"finish_reason":"stop"}]}`, code: "empty_output"},
		{name: "refusal", body: `{"choices":[{"message":{"role":"assistant","content":null,"refusal":"cannot answer"},"finish_reason":"stop"}]}`, code: "refusal"},
		{name: "error payload", body: `{"error":{"code":"unsupported_parameter","message":"bad effort"}}`, code: "unsupported_parameter"},
		{name: "unexpected tool", body: `{"choices":[{"message":{"role":"assistant","content":"21","tool_calls":[{"function":{"name":"exec"}}]},"finish_reason":"stop"}]}`, code: "unexpected_tool_call"},
		{name: "legacy streaming function", body: "data: {\"choices\":[{\"delta\":{\"content\":\"21\",\"function_call\":{\"name\":\"exec\"}},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", code: "unexpected_tool_call"},
		{name: "non-assistant message", body: `{"choices":[{"message":{"role":"user","content":"21"},"finish_reason":"stop"}]}`, code: "invalid_response"},
		{name: "malformed", body: `{not json}`, code: "invalid_response"},
		{name: "too large", body: evalChatJSON(strings.Repeat("x", openAIEvalResponseLimit), "stop"), code: "response_too_large"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := newJSONResponse(http.StatusOK, tc.body)
			defer response.Body.Close()
			result, err := readOpenAIEvalChatCompletionsResponse(t.Context(), response)
			if tc.code != "" {
				var failure *OpenAIEvalRequestError
				require.ErrorAs(t, err, &failure)
				require.Equal(t, tc.code, failure.Code)
				require.Zero(t, result.CompletedAt)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.answer, result.Text)
			require.False(t, result.CompletedAt.IsZero())
			if tc.name != "content parts" {
				require.EqualValues(t, 7, result.InputTokens)
				require.EqualValues(t, 2, result.OutputTokens)
			}
		})
	}
}

func TestEvalNamedPrismAPIAccountRunsAllTextTests(t *testing.T) {
	for _, tc := range []struct {
		testType string
		requests int
	}{{OpenAIEvalTypeCandy, 1}, {OpenAIEvalTypeFingerprint, 60}, {OpenAIEvalTypeModelTrace, 3}} {
		t.Run(tc.testType, func(t *testing.T) {
			svc, repo, upstream := evalRunHarness(t, func(req *http.Request, _ int) (*http.Response, error) {
				require.Equal(t, "/v1/chat/completions", req.URL.Path)
				require.Equal(t, "en-US,en;q=0.9", req.Header.Get("Accept-Language"))
				var payload map[string]any
				require.NoError(t, json.NewDecoder(req.Body).Decode(&payload))
				require.Equal(t, "gpt-5.4", payload["model"])
				require.Equal(t, "medium", payload["reasoning_effort"])
				require.Equal(t, false, payload["stream"])
				require.Len(t, payload["messages"], 2)
				require.NotContains(t, payload, "input")
				require.NotContains(t, payload, "reasoning")
				answer := "21"
				switch tc.testType {
				case OpenAIEvalTypeFingerprint:
					messages := payload["messages"].([]any)
					prompt := messages[1].(map[string]any)["content"].(string)
					matched := false
					for _, probe := range OpenAIEvalFingerprintProbes {
						for _, question := range probe.Prompts {
							if prompt == probe.Instructions+"\n"+question {
								switch probe.Kind {
								case "int":
									answer = fmt.Sprint(probe.Low)
								case "coin":
									answer = "heads"
								case "letter":
									answer = "a"
								default:
									answer = "blue"
								}
								matched = true
							}
						}
					}
					require.True(t, matched, "unknown fingerprint prompt")
				case OpenAIEvalTypeModelTrace:
					answer = strings.TrimSuffix(strings.Repeat("42,", 332), ",")
				}
				return newJSONResponse(200, evalChatJSON(answer, "stop")), nil
			})
			account, err := svc.accounts.GetByID(t.Context(), 995)
			require.NoError(t, err)
			account.Name = "prism"
			account.Extra["openai_responses_supported"] = false
			run, err := svc.Run(t.Context(), OpenAIEvalRunRequest{AccountID: 995, TestType: tc.testType,
				RequestedModel: "gpt-5.4", ReasoningEffort: "medium", SampleMode: "quick"}, 1, "manual")
			require.NoError(t, err)
			require.Equal(t, tc.requests, run.RequestCount)
			require.EqualValues(t, tc.requests, upstream.calls.Load())
			require.Equal(t, tc.requests, run.Outcome.SampleCount)
			require.Len(t, repo.runs, 1)
			require.NotEqual(t, "insufficient", run.Status)
			require.Empty(t, run.Error)
			if tc.testType == OpenAIEvalTypeCandy {
				require.Equal(t, "pass", run.Status)
			}
		})
	}
}

func TestEvalChatFailureRetryAndStateProbeBoundary(t *testing.T) {
	svc, _, upstream := evalRunHarness(t, func(_ *http.Request, call int) (*http.Response, error) {
		if call == 1 {
			return newJSONResponse(503, `{"error":{"code":"server_error","message":"busy"}}`), nil
		}
		return newJSONResponse(200, evalChatJSON("21", "stop")), nil
	})
	account, err := svc.accounts.GetByID(t.Context(), 995)
	require.NoError(t, err)
	account.Extra["openai_responses_supported"] = false
	run, err := svc.Run(t.Context(), OpenAIEvalRunRequest{AccountID: 995, TestType: OpenAIEvalTypeCandy, RequestedModel: "gpt-5.4"}, 1, "manual")
	require.NoError(t, err)
	require.Equal(t, "pass", run.Status)
	require.EqualValues(t, 2, upstream.calls.Load())
	require.Equal(t, 2, run.Samples[0].Attempts)
	_, err = svc.Run(t.Context(), OpenAIEvalRunRequest{AccountID: 995, TestType: OpenAIEvalTypeStateProbe, RequestedModel: "gpt-5.4"}, 1, "manual")
	require.ErrorContains(t, err, "direct OpenAI OAuth account")
	require.EqualValues(t, 2, upstream.calls.Load())
}

func TestEvalProtocolOverrideAndModelMapping(t *testing.T) {
	for _, tc := range []struct {
		name, mode, path string
		supported        bool
	}{
		{"auto Chat Completions", "", "/v1/chat/completions", false},
		{"auto Responses", "", "/v1/responses", true},
		{"force Chat Completions", "force_chat_completions", "/v1/chat/completions", true},
		{"force Responses", "force_responses", "/v1/responses", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, _ := evalRunHarness(t, func(req *http.Request, _ int) (*http.Response, error) {
				require.Equal(t, tc.path, req.URL.Path)
				var payload map[string]any
				require.NoError(t, json.NewDecoder(req.Body).Decode(&payload))
				require.Equal(t, "gpt-5.4", payload["model"])
				if tc.path == "/v1/chat/completions" {
					require.Equal(t, "high", payload["reasoning_effort"])
					return newJSONResponse(200, evalChatJSON("21", "stop")), nil
				}
				require.Equal(t, map[string]any{"effort": "high"}, payload["reasoning"])
				return newJSONResponse(200, evalCompletedJSON("21")), nil
			})
			account, err := svc.accounts.GetByID(t.Context(), 995)
			require.NoError(t, err)
			account.Credentials["model_mapping"] = map[string]any{"gpt-6.1-sol": "gpt-5.4"}
			account.Extra["openai_responses_supported"] = tc.supported
			account.Extra["openai_responses_mode"] = tc.mode
			run, err := svc.Run(t.Context(), OpenAIEvalRunRequest{AccountID: 995, TestType: OpenAIEvalTypeCandy, RequestedModel: "gpt-6.1-sol", ReasoningEffort: "high"}, 1, "manual")
			require.NoError(t, err)
			require.Equal(t, "gpt-6.1-sol", run.RequestedModel)
			require.Equal(t, "gpt-5.4", run.UpstreamModel)
			require.Equal(t, "pass", run.Status)
		})
	}
}

func TestEvalChatDeterministicErrorsKeepSanitizedDetails(t *testing.T) {
	svc, _, upstream := evalRunHarness(t, func(*http.Request, int) (*http.Response, error) {
		return newJSONResponse(400, `{"error":{"code":"unsupported_parameter","message":"reasoning effort unsupported; eval-key; Bearer arbitrary-token"}}`), nil
	})
	account, err := svc.accounts.GetByID(t.Context(), 995)
	require.NoError(t, err)
	account.Extra["openai_responses_supported"] = false
	run, err := svc.Run(t.Context(), OpenAIEvalRunRequest{AccountID: 995, TestType: OpenAIEvalTypeCandy, RequestedModel: "gpt-5.4"}, 1, "manual")
	require.NoError(t, err)
	require.EqualValues(t, 1, upstream.calls.Load())
	require.Equal(t, "unsupported_parameter", run.Samples[0].ErrorCode)
	require.Equal(t, 400, run.Samples[0].HTTPStatus)
	require.Contains(t, run.Samples[0].ErrorMessage, "reasoning effort unsupported")
	require.NotContains(t, run.Samples[0].ErrorMessage, "eval-key")
	require.NotContains(t, run.Samples[0].ErrorMessage, "arbitrary-token")
}
