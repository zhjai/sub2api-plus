package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type timezoneEvalWireUpstream struct {
	*httpUpstreamRecorder
	logicalID  int64
	singleSend bool
	admission  bool
}

func (*timezoneEvalWireUpstream) SupportsSingleSend() bool { return true }
func (u *timezoneEvalWireUpstream) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	u.logicalID = id
	u.singleSend = HTTPUpstreamSingleSendRequired(req.Context())
	policy, ok := req.Context().Value(accountRPMRetryAdmissionKey{}).(accountRPMHTTPPolicy)
	u.admission = ok && policy.admit != nil
	return u.httpUpstreamRecorder.Do(req, proxy, id, concurrency)
}

// Exercise the actual eval dispatch, whose native branch sends directly through
// DoWithTLS instead of the production accountRPMSendHTTP tail hook.
func TestUpstreamClientTimezoneEvalNativeDispatch(t *testing.T) {
	for _, tc := range []struct {
		name, kind         string
		separateCredential bool
		unboundPlugin      bool
	}{
		{name: "apikey", kind: AccountTypeAPIKey},
		{name: "native_oauth_without_plugin", kind: AccountTypeOAuth},
		{name: "logical_settings_override_credential", kind: AccountTypeOAuth, separateCredential: true},
		{name: "logical_settings_with_unbound_plugin", kind: AccountTypeOAuth, separateCredential: true, unboundPlugin: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := timezoneTestAccount("manual", "UTC")
			account.ID = 8901
			account.Type = tc.kind
			account.Concurrency = 2
			credential := account
			if tc.separateCredential {
				credential = timezoneTestAccount("manual", "Asia/Tokyo")
				credential.ID = 8902
				credential.Type = tc.kind
			}
			raw := []byte(`{"model":"gpt-5.4","previous_response_id":"resp_keep","input":[{"role":"user","internal_chat_message_metadata_passthrough":{"content_item_kinds":["environments.environment_context","text"]},"content":[{"type":"input_text","text":"<environment_context><timezone>Asia/Shanghai</timezone><current_date>2020-01-01</current_date></environment_context>"},{"type":"input_text","text":"task timezone Asia/Shanghai"}]},{"type":"function_call","name":"example","call_id":"call_keep","arguments":"{\"timezone\":\"Asia/Shanghai\"}"}]}`)
			req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://eval.invalid/v1/responses", bytes.NewReader(raw))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")
			wire := &timezoneEvalWireUpstream{httpUpstreamRecorder: &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader([]byte(`{}`)))}}}
			svc := &AccountTestService{httpUpstream: wire, tlsFPProfileService: &TLSFingerprintProfileService{}}
			if tc.unboundPlugin {
				svc.pluginManager = &PluginManager{}
			}
			resp, err := svc.doOpenAIEvalUpstream(req, "http://selected-proxy.invalid:8080", account, credential)
			require.NoError(t, err)
			require.NotNil(t, resp)
			defer resp.Body.Close()
			require.Len(t, wire.bodies, 1)
			require.Contains(t, gjson.GetBytes(wire.lastBody, "input.0.content.0.text").String(), "<timezone>UTC</timezone>")
			require.Contains(t, gjson.GetBytes(wire.lastBody, "input.0.content.0.text").String(), "<current_date>2020-01-01</current_date>")
			require.Equal(t, "task timezone Asia/Shanghai", gjson.GetBytes(wire.lastBody, "input.0.content.1.text").String())
			require.Equal(t, `{"timezone":"Asia/Shanghai"}`, gjson.GetBytes(wire.lastBody, "input.1.arguments").String())
			require.Equal(t, "resp_keep", gjson.GetBytes(wire.lastBody, "previous_response_id").String())
			require.Equal(t, "call_keep", gjson.GetBytes(wire.lastBody, "input.1.call_id").String())
			require.Equal(t, account.ID, wire.logicalID)
			require.True(t, wire.singleSend)
			require.True(t, wire.admission)
			require.Equal(t, "http://selected-proxy.invalid:8080", wire.lastProxyURL)
		})
	}
}
