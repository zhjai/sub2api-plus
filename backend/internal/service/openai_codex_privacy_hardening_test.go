package service

import (
	"bytes"
	"context"
	"fmt"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"io"
	"net/http"
	"testing"
)

func privacyReplayBytes(t *testing.T, req *http.Request) []byte {
	t.Helper()
	r, err := req.GetBody()
	require.NoError(t, err)
	defer r.Close()
	raw, err := io.ReadAll(r)
	require.NoError(t, err)
	return raw
}

func TestCodexPrivacyRepeatedFinalizationCleansLateMetadata(t *testing.T) {
	for _, kind := range []string{AccountTypeOAuth, AccountTypeSetupToken, AccountTypeAPIKey} {
		t.Run(kind, func(t *testing.T) {
			a := privacyTestAccount(kind)
			req, err := http.NewRequest("POST", "https://example.test/responses", bytes.NewReader(privacyTestBody(t, "same-session")))
			require.NoError(t, err)
			req.Header.Set("Session-Id", "same-session")
			req.Header.Set("Thread-Id", "client-thread")
			req.Header.Set("X-Client-Trace-Id", privacyTestUUID)
			req.Header.Set("Authorization", "Bearer test-token")
			req.Header.Set(openAIWSTurnStateHeader, "opaque-ticket")
			req = stampCodexPrivacySource(req, a, a, 77)
			first, err := applyCodexPrivacyBeforeSend(req, a)
			require.NoError(t, err)
			before := privacyReplayBytes(t, first)
			late := first.Clone(first.Context())
			late.Header["originator"] = []string{"late-private-originator"}
			late.Header.Set("X-Codex-Hostname", "late-private-host")
			late.Header.Set("Accept-Language", "zh-CN")
			late.Header.Add("X-Client-Trace-Id", "3896911b-4edf-4790-9b2f-7a0ce6d21218")
			late.Header.Set(openAIWSTurnMetadataHeader, "{\"thread_id\":\"client-thread\",\"cwd\":\"/late/private\"}")
			changed, err := sjson.SetBytes(before, "client_metadata.hostname", "late-private-host")
			require.NoError(t, err)
			changed, err = sjson.SetBytes(changed, "client_metadata.turn_id", "late-turn")
			require.NoError(t, err)
			late.Body = io.NopCloser(bytes.NewReader(changed))
			late.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(changed)), nil }
			late.ContentLength = int64(len(changed))
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Body: http.NoBody}}
			svc := &OpenAIGatewayService{httpUpstream: upstream}
			_, err = svc.doOpenAIUpstreamWithAdmission(late, "", a)
			require.NoError(t, err)
			final := upstream.lastReq
			require.Empty(t, final.Header.Get("X-Codex-Hostname"))
			require.Equal(t, codexOutboundAcceptLanguage, final.Header.Get("Accept-Language"))
			require.NotContains(t, string(upstream.lastBody), "late-private-host")
			require.NotContains(t, final.Header.Get(openAIWSTurnMetadataHeader), "/late/private")
			require.Equal(t, first.Header.Get("Session-Id"), final.Header.Get("Session-Id"))
			require.Equal(t, first.Header.Get("Thread-Id"), final.Header.Get("Thread-Id"))
			require.Equal(t, first.Header.Get("X-Client-Trace-Id"), final.Header.Values("X-Client-Trace-Id")[0])
			require.NotEqual(t, "3896911b-4edf-4790-9b2f-7a0ce6d21218", final.Header.Values("X-Client-Trace-Id")[1])
			for _, field := range []string{"prompt_cache_key", "client_metadata.session_id", "client_metadata.thread_id", "client_metadata.installation_id", "input", "instructions", "tools", "previous_response_id"} {
				require.Equal(t, gjson.GetBytes(before, field).Raw, gjson.GetBytes(upstream.lastBody, field).Raw, field)
			}
			p, err := newCodexPrivacyProjection(a, 77)
			require.NoError(t, err)
			require.Equal(t, p.id("turn", "late-turn"), gjson.GetBytes(upstream.lastBody, "client_metadata.turn_id").String())
			require.Equal(t, final.Header.Get("Thread-Id"), gjson.Get(final.Header.Get(openAIWSTurnMetadataHeader), "thread_id").String())
			require.Equal(t, "opaque-ticket", final.Header.Get(openAIWSTurnStateHeader))
			require.Equal(t, "Bearer test-token", final.Header.Get("Authorization"))
			again, err := applyCodexPrivacyBeforeSend(final, a)
			require.NoError(t, err)
			require.Same(t, final, again)
			var wire bytes.Buffer
			writable := final.Clone(final.Context())
			writable.Body, err = final.GetBody()
			require.NoError(t, err)
			require.NoError(t, writable.Write(&wire))
			require.NotContains(t, wire.String(), "late-private-originator")
			require.Equal(t, before, privacyReplayBytes(t, first), "previous snapshot remains unchanged")
		})
	}
}

func TestCodexPrivacyAdditionalEnvironmentHeaders(t *testing.T) {
	p, err := newCodexPrivacyProjection(privacyTestAccount(AccountTypeAPIKey), 77)
	require.NoError(t, err)
	h := http.Header{}
	for _, name := range []string{"x-client-platform", "X-Client-Architecture", "x-client-cwd", "X-Client-Working-Directory", "x-codex-workdir", "X-Codex-Working-Directory", "x-opencode-cwd", "X-Opencode-Hostname", "x-opencode-os", "X-Opencode-Arch"} {
		h[name] = []string{"private-environment"}
	}
	h.Set("X-Vendor-Opaque", privacyTestUUID)
	h.Set("X-Client-Auth-Key", privacyTestUUID)
	h.Set(openAIWSTurnStateHeader, "opaque-ticket")
	p.headers(h)
	for _, values := range h {
		require.NotContains(t, values, "private-environment")
	}
	require.Equal(t, privacyTestUUID, h.Get("X-Vendor-Opaque"))
	require.Equal(t, privacyTestUUID, h.Get("X-Client-Auth-Key"))
	require.Equal(t, "opaque-ticket", h.Get(openAIWSTurnStateHeader))
}

func TestCodexPrivacyBinaryJSONFrames(t *testing.T) {
	p, err := newCodexPrivacyProjection(privacyTestAccount(AccountTypeOAuth), 77)
	require.NoError(t, err)
	raw := privacyTestBody(t, "binary-session")
	capture := &privacyTypedCaptureWS{}
	frame := &codexPrivacyFrameConn{FrameConn: capture, projection: p}
	require.NoError(t, frame.WriteFrame(context.Background(), coderws.MessageBinary, raw))
	expected, err := p.body(raw)
	require.NoError(t, err)
	require.Equal(t, expected, capture.wire[0])
	require.Equal(t, coderws.MessageBinary, capture.kinds[0])
	require.Equal(t, "resp_continue", gjson.GetBytes(capture.wire[0], "previous_response_id").String())
	for _, raw := range [][]byte{{0, 255, 33}, []byte("null"), []byte("[1,2]")} {
		require.NoError(t, frame.WriteFrame(context.Background(), coderws.MessageBinary, raw))
		require.Equal(t, raw, capture.wire[len(capture.wire)-1])
	}
}

func TestCodexPrivacyProjectedIDTrustIsRequestLocalAndKindScoped(t *testing.T) {
	a := privacyTestAccount(AccountTypeAPIKey)
	req, err := http.NewRequest("POST", "https://example.test/responses", bytes.NewReader(privacyTestBody(t, "same-session")))
	require.NoError(t, err)
	req = stampCodexPrivacySource(req, a, a, 77)
	clean, err := applyCodexPrivacyBeforeSend(req, a)
	require.NoError(t, err)
	projectedSession := gjson.GetBytes(privacyReplayBytes(t, clean), "client_metadata.session_id").String()
	p, err := newCodexPrivacyProjection(a, 77)
	require.NoError(t, err)
	// A fresh client cannot declare a UUID protected merely because it appeared
	// in another request, or reuse an output from a different identity kind.
	fresh, err := http.NewRequest("POST", "https://example.test/responses", bytes.NewReader(privacyTestBody(t, projectedSession)))
	require.NoError(t, err)
	fresh = stampCodexPrivacySource(fresh, a, a, 77)
	fresh, err = applyCodexPrivacyBeforeSend(fresh, a)
	require.NoError(t, err)
	require.Equal(t, p.id("session", projectedSession), gjson.GetBytes(privacyReplayBytes(t, fresh), "client_metadata.session_id").String())
	require.NotEqual(t, projectedSession, gjson.GetBytes(privacyReplayBytes(t, fresh), "client_metadata.session_id").String())
	next := clean.Clone(clean.Context())
	next.Header.Set("Thread-Id", projectedSession)
	next, err = applyCodexPrivacyBeforeSend(next, a)
	require.NoError(t, err)
	require.Equal(t, p.id("thread", projectedSession), next.Header.Get("Thread-Id"))
}

func TestCodexPrivacyRepeatedFinalizationSharesOnlyImmutableState(t *testing.T) {
	a := privacyTestAccount(AccountTypeAPIKey)
	req, err := http.NewRequest("POST", "https://example.test/responses", bytes.NewReader(privacyTestBody(t, "same-session")))
	require.NoError(t, err)
	req = stampCodexPrivacySource(req, a, a, 77)
	req, err = applyCodexPrivacyBeforeSend(req, a)
	require.NoError(t, err)
	original := req.Context().Value(codexPrivacyAppliedKey{}).(*codexPrivacyAppliedState)
	size := len(original.projectedIDs)
	for i := 0; i < 8; i++ {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			t.Parallel()
			clone := req.Clone(req.Context())
			var e error
			clone.Body, e = clone.GetBody()
			require.NoError(t, e)
			defer clone.Body.Close()
			clone.Header.Set("X-Client-New-Id", fmt.Sprintf("a4e59951-6f88-437b-a05d-%012d", i))
			clone.Header.Set("X-Codex-Hostname", "late-private-host")
			out, e := applyCodexPrivacyBeforeSend(clone, a)
			require.NoError(t, e)
			require.Empty(t, out.Header.Get("X-Codex-Hostname"))
			require.Equal(t, size, len(original.projectedIDs), "previous context map is immutable")
			require.NotEqual(t, clone.Header.Get("X-Client-New-Id"), out.Header.Get("X-Client-New-Id"))
			require.NoError(t, out.Body.Close())
		})
	}
}

type privacyTypedCaptureWS struct {
	privacyCaptureWS
	kinds []coderws.MessageType
}

func (c *privacyTypedCaptureWS) WriteFrame(ctx context.Context, kind coderws.MessageType, raw []byte) error {
	c.kinds = append(c.kinds, kind)
	return c.privacyCaptureWS.WriteFrame(ctx, kind, raw)
}
