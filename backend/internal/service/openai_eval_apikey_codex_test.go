//go:build unit

package service

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestEvalAPIKeyCodexFinalWireAndIndependentRetries(t *testing.T) {
	var bodies [][]byte
	upstream := &evalTransportStub{respond: func(req *http.Request, attempt int) (*http.Response, error) {
		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		bodies = append(bodies, body)
		require.Equal(t, "https://eval.example/v1/responses", req.URL.String())
		require.Equal(t, "Bearer eval-key", req.Header.Get("Authorization"))
		require.Empty(t, req.Header.Get("ChatGPT-Account-Id"))
		require.Empty(t, req.Header.Get("OpenAI-Beta"))
		require.Equal(t, "text/event-stream", req.Header.Get("Accept"))
		require.Equal(t, "true", req.Header.Get(responsesLiteHeader))
		require.Equal(t, "en-US,en;q=0.9", req.Header.Get("Accept-Language"))
		require.NotEmpty(t, req.Header.Get("Originator"))
		require.NotEmpty(t, req.Header.Get("Version"))
		require.Equal(t, "gpt-6.1-sol", gjson.GetBytes(body, "model").String())
		require.Equal(t, "high", gjson.GetBytes(body, "reasoning.effort").String())
		require.Equal(t, "all_turns", gjson.GetBytes(body, "reasoning.context").String())
		require.True(t, gjson.GetBytes(body, "stream").Bool())
		require.False(t, gjson.GetBytes(body, "store").Bool())
		require.False(t, gjson.GetBytes(body, "instructions").Exists())
		require.False(t, gjson.GetBytes(body, "max_output_tokens").Exists())
		require.False(t, gjson.GetBytes(body, "previous_response_id").Exists())
		require.Equal(t, "additional_tools", gjson.GetBytes(body, "input.0.type").String())
		require.True(t, gjson.GetBytes(body, "input.0.tools.#(name==\"functions\").tools.#(name==\"exec\")").Exists())
		require.Greater(t, len(gjson.GetBytes(body, "input.1.content.0.text").String()), 20000)
		require.Contains(t, gjson.GetBytes(body, "input.6.content.0.text").String(), "probe question")
		require.NotContains(t, string(body), "eval-key")
		require.NotContains(t, string(body), "/home/zhj")
		for header, field := range map[string]string{
			"Session-Id": "session_id", "Thread-Id": "thread_id",
			"X-Client-Request-Id": "x-client-request-id", "X-Codex-Window-Id": "x-codex-window-id",
			"X-Codex-Installation-Id": "x-codex-installation-id", openAIWSTurnMetadataHeader: openAIWSTurnMetadataHeader,
		} {
			require.NotEmpty(t, req.Header.Get(header))
			require.Equal(t, gjson.GetBytes(body, "client_metadata."+field).String(), req.Header.Get(header))
		}
		require.Equal(t, gjson.GetBytes(body, "prompt_cache_key").String(), req.Header.Get("Session-Id"))
		if attempt == 1 {
			return newJSONResponse(503, `{"error":{"code":"server_overloaded","message":"retry"}}`), nil
		}
		answer := "21"
		if attempt == 3 {
			answer = "29"
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":" + evalCompletedJSON(answer) + "}\n\n"))}, nil
	}}
	svc, target := evalOAuthHarness(upstream)
	target.Account.Type = AccountTypeAPIKey
	target.Account.Credentials = map[string]any{"api_key": "eval-key", "base_url": "https://eval.example/v1"}
	target.Account.Extra = map[string]any{"openai_responses_supported": true}
	svc.cfg = testConfig()
	target.RequestedModel, target.UpstreamModel = "gpt-6.1-sol", "gpt-6.1-sol"
	first, record, err := svc.runOpenAIEvalSampleAttempts(t.Context(), target, "probe question", "high", 3)
	require.NoError(t, err)
	require.Equal(t, 2, record.Attempts)
	require.Equal(t, "21", first.Text)
	second, err := svc.RunOpenAIEvalSample(t.Context(), target, "probe question", "high")
	require.NoError(t, err)
	require.Equal(t, "29", second.Text, "identical probes must not replay a cached answer")
	require.Len(t, bodies, 3)
	for _, field := range []string{"prompt_cache_key", "client_metadata.session_id", "client_metadata.thread_id", "client_metadata.turn_id", "client_metadata.x-codex-window-id", "input.0.id", "input.6.id"} {
		seen := map[string]bool{}
		for _, body := range bodies {
			value := gjson.GetBytes(body, field).String()
			require.NotEmpty(t, value)
			require.False(t, seen[value], "attempts must have fresh %s", field)
			seen[value] = true
		}
	}
	for _, body := range bodies[1:] {
		require.Equal(t, gjson.GetBytes(bodies[0], "client_metadata.x-codex-installation-id").String(),
			gjson.GetBytes(body, "client_metadata.x-codex-installation-id").String(), "fresh sessions do not rotate the device")
	}
}

func TestEvalAPIKeyCodexRunProfile(t *testing.T) {
	svc, _, _ := evalRunHarness(t, func(*http.Request, int) (*http.Response, error) {
		return newJSONResponse(200, evalCompletedJSON("21")), nil
	})
	run, err := svc.Run(t.Context(), OpenAIEvalRunRequest{AccountID: 995, TestType: OpenAIEvalTypeCandy, RequestedModel: "gpt-5.4"}, 1, "manual")
	require.NoError(t, err)
	require.Equal(t, "pass", run.Status)
	require.Equal(t, openAIEvalAPIKeyCodexRequestProfile, run.Outcome.RequestProfile)
}

func TestEvalAPIKeyAllResponsesTestsUseCodexTurns(t *testing.T) {
	for _, tc := range []struct {
		testType string
		requests int
	}{{OpenAIEvalTypeCandy, 1}, {OpenAIEvalTypeModelTrace, 3}, {OpenAIEvalTypeFingerprint, 60}} {
		t.Run(tc.testType, func(t *testing.T) {
			svc, _, transport := evalRunHarness(t, func(req *http.Request, _ int) (*http.Response, error) {
				body, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				require.Equal(t, "/v1/responses", req.URL.Path)
				require.True(t, gjson.GetBytes(body, "stream").Bool())
				require.Equal(t, "additional_tools", gjson.GetBytes(body, "input.0.type").String())
				prompt := gjson.GetBytes(body, "input.6.content.0.text").String()
				require.Contains(t, prompt, "Do not use any external tools")
				answer := "21"
				switch tc.testType {
				case OpenAIEvalTypeModelTrace:
					answer = strings.TrimSuffix(strings.Repeat("42,", 332), ",")
				case OpenAIEvalTypeCandy:
					require.Equal(t, "Do not use any external tools to answer the following request.\n\n"+OpenAIEvalCandyPrompt, prompt)
				case OpenAIEvalTypeFingerprint:
					matched := false
					for _, probe := range OpenAIEvalFingerprintProbes {
						for _, question := range probe.Prompts {
							if prompt != "Do not use any external tools to answer the following request.\n\n"+probe.Instructions+"\n"+question {
								continue
							}
							matched = true
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
						}
					}
					require.True(t, matched)
				}
				return newJSONResponse(200, evalCompletedJSON(answer)), nil
			})
			run, err := svc.Run(t.Context(), OpenAIEvalRunRequest{AccountID: 995, TestType: tc.testType, RequestedModel: "gpt-5.4", SampleMode: "quick"}, 1, "manual")
			require.NoError(t, err)
			require.Equal(t, tc.requests, run.RequestCount)
			require.EqualValues(t, tc.requests, transport.calls.Load())
			require.Equal(t, tc.requests, run.Outcome.SampleCount)
			require.Equal(t, openAIEvalAPIKeyCodexRequestProfile, run.Outcome.RequestProfile)
		})
	}
}
