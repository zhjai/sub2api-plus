//go:build unit

package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type r8WireUpstream struct{ *httpUpstreamRecorder }

func (*r8WireUpstream) Do(r *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return http.DefaultClient.Do(r)
}

func (u *r8WireUpstream) DoWithTLS(r *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(r, proxy, id, concurrency)
}

func TestR8CodexEnglishWireAcrossFallbackAndModelDiscovery(t *testing.T) {
	for _, forced := range []bool{false, true} {
		t.Run(map[bool]string{false: "lowercase_effective_originator", true: "force_cli"}[forced], func(t *testing.T) {
			headers := make(chan http.Header, 8)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				headers <- r.Header.Clone()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			}))
			defer server.Close()
			account := &Account{ID: 71, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"api_key": "synthetic-token", "base_url": server.URL,
					credKeyHeaderOverrideEnabled: true,
					credKeyHeaderOverrides:       map[string]any{"accept-language": "zh-CN", "originator": "codex_cli_rs", "User-Agent": "generic-client"}}}
			c, _ := newTestContext()
			c.Request.Header = http.Header{"User-Agent": {"generic-client"}, "aCcEpT-LaNgUaGe": {"zh-TW"}}
			cfg := &config.Config{}
			cfg.Gateway.ForceCodexCLI = forced
			cfg.Security.URLAllowlist.AllowInsecureHTTP = true
			svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: &r8WireUpstream{}}
			body := []byte(`{"model":"gpt-6-astra","stream":false}`)
			for _, passthrough := range []bool{false, true} {
				var request *http.Request
				var err error
				if passthrough {
					request, err = svc.buildUpstreamRequestOpenAIPassthrough(t.Context(), c, account, body, "synthetic-token")
				} else {
					request, err = svc.buildUpstreamRequest(t.Context(), c, account, body, "synthetic-token", false, "", true)
				}
				require.NoError(t, err)
				response, err := http.DefaultClient.Do(request)
				require.NoError(t, err)
				_ = response.Body.Close()
				require.Equal(t, []string{"en-US,en;q=0.9"}, (<-headers).Values("Accept-Language"))
			}
			response, err := svc.sendCCUpstreamRequest(t.Context(), c, account, server.URL+"/v1/chat/completions", body, false, "synthetic-token", "generic-client", "")
			require.NoError(t, err)
			_ = response.Body.Close()
			require.Equal(t, []string{"en-US,en;q=0.9"}, (<-headers).Values("Accept-Language"))
			request, err := buildOpenAIAPIKeyModelsRequest(t.Context(), account, func(base string) (string, error) { return base, nil })
			require.NoError(t, err)
			response, err = http.DefaultClient.Do(request)
			require.NoError(t, err)
			_ = response.Body.Close()
			require.Equal(t, []string{"en-US,en;q=0.9"}, (<-headers).Values("Accept-Language"))
		})
	}
}

func TestR8WSIntegrityEvidenceFailureAndControls(t *testing.T) {
	contract := []byte(`{"model":"gpt-6-astra","tools":[{"type":"custom","name":"exec"}]}`)
	for _, tc := range []struct {
		name, terminal, status, reason                       string
		cancelled, exec, missingContract, meaningful, escape bool
	}{
		{name: "denial_completed", terminal: "response.completed", status: "completed", meaningful: true, escape: true},
		{name: "terminal_exec_controls_denial", terminal: "response.completed", status: "completed", exec: true, meaningful: true},
		{name: "no_outbound_exec_contract", terminal: "response.completed", status: "completed", missingContract: true, meaningful: true},
		{name: "partial_eof", status: "premature_eof", reason: "stream_terminated", meaningful: true, escape: true},
		{name: "incomplete_terminated", terminal: "response.incomplete", status: "incomplete", reason: "stream_terminated", meaningful: true, escape: true},
		{name: "max_output_tokens", terminal: "response.incomplete", status: "incomplete", reason: "max_output_tokens", meaningful: true},
		{name: "client_cancelled", cancelled: true, meaningful: true},
		{name: "preoutput_eof"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outbound := contract
			if tc.missingContract {
				outbound = []byte(`{"model":"gpt-6-astra"}`)
			}
			evidence := newOpenAIWSIntegrityEvidence(contract, outbound)
			if tc.meaningful {
				payload := []byte(`{"type":"response.output_text.delta","response_id":"resp_r8_partial","delta":"当前会话没有可用的终端工具"}`)
				evidence.observe("response.output_text.delta", payload)
				evidence.delivered("response.output_text.delta", payload)
			}
			if tc.terminal != "" {
				output := `[]`
				if tc.exec {
					output = `[{"type":"custom_tool_call","name":"exec","input":"pwd"}]`
				}
				payload := []byte(`{"type":"` + tc.terminal + `","response":{"id":"resp_r8_partial","output":` + output + `,"incomplete_details":{"reason":"` + tc.reason + `"}}}`)
				evidence.observe(tc.terminal, payload)
				evidence.delivered(tc.terminal, payload)
			}
			result := &OpenAIForwardResult{UpstreamTerminalEvent: tc.terminal}
			evidence.apply(result, tc.terminal == "", tc.cancelled)
			require.Equal(t, tc.status, result.ResponsesProtocolStatus)
			require.Equal(t, tc.reason, result.ResponsesIncompleteReason)
			require.Equal(t, tc.escape, result.RequiresSessionAccountEscape())
			require.Equal(t, tc.cancelled, result.ClientDisconnect)
			require.Equal(t, tc.exec, result.ExecCallObserved)
			fresh := newOpenAIWSIntegrityEvidence(contract, contract)
			reset := &OpenAIForwardResult{}
			fresh.apply(reset, true, false)
			require.False(t, reset.RequiresSessionAccountEscape(), "next turn must not inherit previous evidence")
		})
	}
}

func TestR8TerminalOnlyExecEvidence(t *testing.T) {
	contract := []byte(`{"tools":[{"type":"custom","name":"exec"}]}`)
	for _, actualCall := range []bool{false, true} {
		evidence := newOpenAIWSIntegrityEvidence(contract, contract)
		output := `[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"当前会话没有可用的终端工具"}]}]`
		if actualCall {
			output = `[{"type":"custom_tool_call","name":"exec","input":"pwd"}]`
		}
		payload := []byte(`{"type":"response.completed","response":{"id":"resp_terminal_only","output":` + output + `}}`)
		evidence.observe("response.completed", payload)
		evidence.delivered("response.completed", payload)
		result := &OpenAIForwardResult{UpstreamTerminalEvent: "response.completed"}
		evidence.apply(result, false, false)
		require.Equal(t, !actualCall, result.ToolCapabilityFailure)
		require.Equal(t, actualCall, result.ExecCallObserved)
		require.Equal(t, "resp_terminal_only", result.RequestID)
	}
}
