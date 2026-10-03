//go:build unit

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestR10DonePayloadStatusGatesCapabilityEvidence(t *testing.T) {
	contract := []byte(`{"tools":[{"type":"custom","name":"exec"}]}`)
	for _, tc := range []struct {
		status, reason  string
		failure, escape bool
	}{
		{status: "completed", failure: true, escape: true},
		{status: "", failure: true, escape: true},
		{status: "incomplete", reason: "max_output_tokens"},
		{status: "failed"},
		{status: "terminated"},
		{status: "cancelled"},
		{status: "canceled"},
		{status: "incomplete", reason: "stream_terminated", escape: true},
	} {
		t.Run(tc.status+"/"+tc.reason, func(t *testing.T) {
			evidence := newOpenAIWSIntegrityEvidence(contract, contract)
			payload := []byte(fmt.Sprintf(`{"type":"response.done","response":{"id":"resp_r10_done","status":%q,"incomplete_details":{"reason":%q},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"The terminal tool is not available in this session."}]}]}}`, tc.status, tc.reason))
			evidence.observe("response.done", payload)
			evidence.delivered("response.done", payload)
			result := &OpenAIForwardResult{UpstreamTerminalEvent: "response.done"}
			evidence.apply(result, false, false)
			status := tc.status
			if status == "" {
				status = "completed"
			}
			if status == "canceled" {
				status = "cancelled"
			}
			if status == "terminated" {
				status = "failed"
			}
			require.Equal(t, status, result.ResponsesProtocolStatus)
			require.Equal(t, tc.reason, result.ResponsesIncompleteReason)
			require.Equal(t, tc.failure, result.ToolCapabilityFailure)
			require.Equal(t, tc.escape, result.RequiresSessionAccountEscape())
			if status == "failed" {
				require.False(t, result.SucceededForScheduling())
			}
		})
	}
}

func TestR10WSLateExecLeakAndEvidenceControls(t *testing.T) {
	contract := []byte(`{"tools":[{"type":"custom","name":"exec"}]}`)
	for _, tc := range []struct {
		name                      string
		realExec, missingOutbound bool
	}{
		{name: "late_fragmented_leak"},
		{name: "terminal_real_exec", realExec: true},
		{name: "outbound_not_declared", missingOutbound: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outbound := contract
			if tc.missingOutbound {
				outbound = []byte(`{}`)
			}
			evidence := newOpenAIWSIntegrityEvidence(contract, outbound)
			for _, fragment := range []string{"Checking the workspace.\n", "to=functions.", "exec code:\n", `{"cmd":"pwd"}`} {
				payload := []byte(fmt.Sprintf(`{"type":"response.output_text.delta","response_id":"resp_r10_leak","delta":%q}`, fragment))
				evidence.observe("response.output_text.delta", payload)
				evidence.delivered("response.output_text.delta", payload)
			}
			output := `[]`
			if tc.realExec {
				output = `[{"type":"custom_tool_call","name":"exec","call_id":"call_r10","input":"pwd"}]`
			}
			terminal := []byte(`{"type":"response.completed","response":{"id":"resp_r10_leak","status":"completed","output":` + output + `}}`)
			evidence.observe("response.completed", terminal)
			evidence.delivered("response.completed", terminal)
			result := &OpenAIForwardResult{UpstreamTerminalEvent: "response.completed"}
			evidence.apply(result, false, false)
			require.Equal(t, !tc.realExec && !tc.missingOutbound, result.ToolCapabilityFailure)
			require.Equal(t, !tc.realExec && !tc.missingOutbound, result.RequiresSessionAccountEscape())
			require.Equal(t, tc.realExec, result.ExecCallObserved)
			require.LessOrEqual(t, len(evidence.protocolGuard.text), openAIExecProtocolPrefixMaxBytes)
			fresh := newOpenAIWSIntegrityEvidence(contract, contract)
			freshResult := &OpenAIForwardResult{}
			fresh.apply(freshResult, false, false)
			require.False(t, freshResult.RequiresSessionAccountEscape())
		})
	}
}

type r10OrderedBody struct {
	ctx              context.Context
	text             *strings.Reader
	closed           chan struct{}
	closeOnce        sync.Once
	cancelledAtClose chan bool
}

func (b *r10OrderedBody) Read(p []byte) (int, error) {
	if b.text.Len() > 0 {
		return b.text.Read(p)
	}
	select {
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	case <-b.closed:
		return 0, io.EOF
	}
}

func (b *r10OrderedBody) Close() error {
	b.closeOnce.Do(func() {
		b.cancelledAtClose <- b.ctx.Err() != nil
		close(b.closed)
	})
	return nil
}

type r10OrderedUpstream struct {
	HTTPUpstream
	event            string
	cancelledAtClose chan bool
}

func (u *r10OrderedUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: &r10OrderedBody{
		ctx: req.Context(), text: strings.NewReader("data: " + u.event + "\n\n"), closed: make(chan struct{}), cancelledAtClose: u.cancelledAtClose,
	}}, nil
}

func TestR10BridgeCancelsTransportBeforeClosingConcurrentToolReader(t *testing.T) {
	for _, converted := range []bool{false, true} {
		for _, mismatch := range []bool{false, true} {
			t.Run(fmt.Sprintf("converted_%v_mismatch_%v", converted, mismatch), func(t *testing.T) {
				upstream := &r10OrderedUpstream{cancelledAtClose: make(chan bool, 1), event: `{"type":"response.completed","response":{"id":"resp_ordered","model":"gpt-5.1","status":"completed","output":[]}}`}
				if mismatch {
					upstream.event = `{"type":"response.created","response":{"id":"resp_ordered","model":"gpt-4.1-mini","status":"in_progress"}}`
				}
				cfg := &config.Config{}
				cfg.Security.URLAllowlist.AllowInsecureHTTP = true
				svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}
				account := &Account{ID: 101, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "http://127.0.0.1:8080", "api_key": "synthetic"}, Extra: map[string]any{"openai_opaque_upstream": true}}
				c, _ := newTestContext()
				tools := `[]`
				if converted {
					tools = `[{"type":"custom","name":"exec"}]`
				}
				payload := []byte(fmt.Sprintf(`{"type":"response.create","model":"gpt-5.1","tools":%s,"input":"hello"}`, tools))
				_, err := svc.proxyOpenAIWSHTTPBridgeTurn(t.Context(), c, account, "synthetic", payload, len(payload), "gpt-5.1", "", "", "", "", 1, func([]byte) error { return nil })
				if mismatch {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
				require.True(t, <-upstream.cancelledAtClose, "cancel transport before Close, including while the tool restorer is reading")
			})
		}
	}
}

func TestR10CodexAncillaryEnglishWireAndNonCodexControl(t *testing.T) {
	for _, codex := range []bool{false, true} {
		for _, locale := range []string{"zh-CN", "en-GB"} {
			t.Run(fmt.Sprintf("codex_%v_%s", codex, locale), func(t *testing.T) {
				headers := make(chan http.Header, 4)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					headers <- r.Header.Clone()
					if strings.Contains(r.URL.Path, "chat/completions") {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprint(w, `{"data":[],"model":"text-embedding-3-small","usage":{"prompt_tokens":1,"total_tokens":1}}`)
				}))
				defer server.Close()
				originator := "generic-client"
				if codex {
					originator = "codex_cli_rs"
				}
				account := &Account{ID: 102, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
					"base_url": server.URL, "api_key": "synthetic", credKeyHeaderOverrideEnabled: true,
					credKeyHeaderOverrides: map[string]any{"aCcEpT-LaNgUaGe": locale, "originator": originator},
				}}
				cfg := &config.Config{}
				cfg.Security.URLAllowlist.AllowInsecureHTTP = true
				upstream := &r8WireUpstream{}
				svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}
				c, _ := newTestContext()
				c.Request.Header = http.Header{"User-Agent": {"generic-client"}, "accept-language": {"zh-TW"}}
				probe := &AccountTestService{cfg: cfg, httpUpstream: upstream}
				require.NoError(t, probe.testOpenAIChatCompletionsConnection(c, account, "gpt-5.1", "hello", server.URL, "synthetic"))
				c, _ = newTestContext()
				request, err := svc.buildOpenAIImagesRequest(t.Context(), c, account, []byte(`{"model":"gpt-image-1","prompt":"test"}`), "application/json", "synthetic", openAIImagesGenerationsEndpoint)
				require.NoError(t, err)
				response, err := http.DefaultClient.Do(request)
				require.NoError(t, err)
				_ = response.Body.Close()
				c, _ = newTestContext()
				_, err = svc.ForwardEmbeddings(t.Context(), c, account, []byte(`{"model":"text-embedding-3-small","input":"test"}`), "")
				require.NoError(t, err)
				want := locale
				if codex {
					want = codexOutboundAcceptLanguage
				}
				for range 3 {
					require.Equal(t, []string{want}, (<-headers).Values("Accept-Language"))
				}
			})
		}
	}
}
