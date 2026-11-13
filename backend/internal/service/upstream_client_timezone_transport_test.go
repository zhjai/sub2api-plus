package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	openaiwsv2 "github.com/Wei-Shaw/sub2api/internal/service/openai_ws_v2"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestUpstreamClientTimezoneHTTPWireBeforeForward(t *testing.T) {
	account := timezoneTestAccount("manual", "America/Los_Angeles")
	body := timezoneTestBody(t, "<environment_context><cwd>/workspace</cwd><timezone>Asia/Shanghai</timezone><current_date>2026-07-01</current_date></environment_context>", true)
	// Deliberately use a one-shot body with no GetBody. Maintenance and test
	// callers use this form; the hook must restore it when reading it once.
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://upstream.invalid/v1/responses", bytes.NewReader(body))
	require.NoError(t, err)
	req.GetBody = nil
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader([]byte(`{}`)))}}
	_, err = accountRPMSendHTTP(upstream, account, req, "", nil)
	require.NoError(t, err)
	got := upstream.lastBody
	require.Contains(t, gjson.GetBytes(got, "input.0.content.0.text").String(), "<timezone>America/Los_Angeles</timezone>")
	require.Contains(t, gjson.GetBytes(got, "input.0.content.0.text").String(), "<current_date>2026-07-01</current_date>")
	require.Equal(t, "resp_keep", gjson.GetBytes(got, "previous_response_id").String())
	require.False(t, gjson.GetBytes(got, "store").Bool())

	plain := timezoneTestBody(t, "ordinary timezone Asia/Shanghai text", false)
	req, err = http.NewRequestWithContext(context.Background(), http.MethodPost, "https://upstream.invalid/v1/responses", bytes.NewReader(plain))
	require.NoError(t, err)
	req.GetBody = nil
	upstream = &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader([]byte(`{}`)))}}
	_, err = accountRPMSendHTTP(upstream, account, req, "", nil)
	require.NoError(t, err)
	require.Equal(t, plain, upstream.lastBody)
}

func TestUpstreamClientTimezoneBPSPrepareBeforeBridge(t *testing.T) {
	account := excelBPSAccount(t)
	account.Extra = map[string]any{UpstreamClientTimezoneModeKey: "manual", UpstreamClientTimezoneKey: "UTC"}
	body := timezoneTestBody(t, "<environment_context><timezone>Asia/Shanghai</timezone><current_date>2026-01-01</current_date></environment_context>", true)
	body = bytes.Replace(body, []byte(`"previous_response_id":"resp_keep","store":false`), []byte(`"model":"client-model","store":false`), 1)
	req, _, err := prepareExcelBPSRequest(context.Background(), body, account.GetOpenAIAccessToken(), account, "", false)
	require.NoError(t, err)
	prepared, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	// The bridge may remove the marker after this hook; the actual wire body
	// must never contain the client timezone if it retains environment XML.
	require.NotContains(t, string(prepared), "<timezone>Asia/Shanghai</timezone>")
	require.Contains(t, string(prepared), "UTC")
	require.NotContains(t, string(prepared), "resp_keep")
}

type timezoneWireFrameConn struct {
	openaiwsv2.FrameConn
	writes [][]byte
	kinds  []coderws.MessageType
}

func (c *timezoneWireFrameConn) ReadFrame(context.Context) (coderws.MessageType, []byte, error) {
	return 0, nil, io.EOF
}
func (c *timezoneWireFrameConn) WriteFrame(_ context.Context, kind coderws.MessageType, payload []byte) error {
	c.kinds = append(c.kinds, kind)
	c.writes = append(c.writes, append([]byte(nil), payload...))
	return nil
}
func (c *timezoneWireFrameConn) Close() error { return nil }

func TestUpstreamClientTimezoneWSFrameWirePreservesProtocolFields(t *testing.T) {
	inner := &timezoneWireFrameConn{}
	wrapped := &upstreamClientTimezoneFrameConn{FrameConn: inner, account: timezoneTestAccount("manual", "UTC")}
	marked := timezoneTestBody(t, "<environment_context><timezone>Asia/Shanghai</timezone><current_date>2026-01-01</current_date></environment_context>", true)
	marked = bytes.Replace(marked, []byte(`"previous_response_id":"resp_keep"`), []byte(`"previous_response_id":"resp_keep","tool_call_id":"call_keep","input_text":"plain timezone Asia/Shanghai"`), 1)
	require.NoError(t, wrapped.WriteFrame(context.Background(), coderws.MessageText, marked))
	require.Len(t, inner.writes, 1)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(inner.writes[0], &decoded))
	require.Equal(t, "resp_keep", decoded["previous_response_id"])
	require.Equal(t, "call_keep", decoded["tool_call_id"])
	require.Equal(t, "plain timezone Asia/Shanghai", decoded["input_text"])
	text := gjson.GetBytes(inner.writes[0], "input.0.content.0.text").String()
	require.Contains(t, text, "<timezone>UTC</timezone>")
	require.Contains(t, text, "<current_date>2026-01-01</current_date>")

	tool := []byte(`{"type":"response.create","tool_choice":{"type":"function","name":"keep"},"arguments":{"timezone":"Asia/Shanghai"}}`)
	require.NoError(t, wrapped.WriteFrame(context.Background(), coderws.MessageText, tool))
	require.Equal(t, tool, inner.writes[1])
	plain := []byte(`not-json websocket text`)
	require.NoError(t, wrapped.WriteFrame(context.Background(), coderws.MessageText, plain))
	require.Equal(t, plain, inner.writes[2])
}

func TestUpstreamClientTimezoneForwardBeforeOAuthMetadataStrip(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(map[bool]string{false: "native", true: "passthrough"}[passthrough], func(t *testing.T) {
			account := newOpenAIOAuthNamespaceTestAccount()
			account.Extra = map[string]any{UpstreamClientTimezoneModeKey: "manual", UpstreamClientTimezoneKey: "UTC", "openai_passthrough": passthrough, "openai_oauth_responses_websockets_v2_mode": OpenAIWSIngressModeOff}
			body := []byte(`{"model":"gpt-5.4","stream":false,"instructions":"test","input":[{"role":"user","internal_chat_message_metadata_passthrough":{"content_item_kinds":["environments.environment_context","text"]},"content":[{"type":"input_text","text":"<environment_context><timezone>Asia/Shanghai</timezone><current_date>2020-01-01</current_date></environment_context>"},{"type":"input_text","text":"task: Asia/Shanghai /home/Asia/Shanghai"}]}]}`)
			upstream := &httpUpstreamRecorder{resp: excelBPSMockResponse(http.StatusOK, excelBPSSuccessWire)}
			_, err := newOpenAIRejectedFieldTestService(upstream).Forward(context.Background(), newOpenAIRejectedFieldTestContext(body), account, body)
			require.NoError(t, err)
			require.Len(t, upstream.bodies, 1)
			wire := upstream.bodies[0]
			require.Contains(t, gjson.GetBytes(wire, "input.0.content.0.text").String(), "<timezone>UTC</timezone>")
			require.Contains(t, gjson.GetBytes(wire, "input.0.content.0.text").String(), "<current_date>2020-01-01</current_date>")
			require.Equal(t, "task: Asia/Shanghai /home/Asia/Shanghai", gjson.GetBytes(wire, "input.0.content.1.text").String())
			require.False(t, gjson.GetBytes(wire, "input.0.internal_chat_message_metadata_passthrough").Exists())
		})
	}
}

func TestUpstreamClientTimezonePassthroughActualFrames(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	upstream := newStagedPassthroughConn()
	account := passthroughLifecycleAccount()
	account.Extra[UpstreamClientTimezoneModeKey] = "manual"
	account.Extra[UpstreamClientTimezoneKey] = "UTC"
	svc := newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream)
	server, serverErr := startPassthroughLifecycleServer(t, ctx, svc, account)
	defer server.Close()
	conn, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	defer conn.CloseNow()
	payload := []byte(`{"type":"response.create","model":"gpt-5.1","input":[{"role":"user","internal_chat_message_metadata_passthrough":{"content_item_kinds":["environments.environment_context","text"]},"content":[{"type":"input_text","text":"<environment_context><timezone>Asia/Shanghai</timezone></environment_context>"},{"type":"input_text","text":"keep Asia/Shanghai"}]}]}`)
	for turn := 0; turn < 2; turn++ {
		next := payload
		if turn == 1 {
			next = bytes.Replace(payload, []byte(`"model":"gpt-5.1"`), []byte(`"model":"gpt-5.1","previous_response_id":"resp_first"`), 1)
		}
		require.NoError(t, conn.Write(ctx, coderws.MessageText, next))
		wire := requirePassthroughUpstreamWrite(t, upstream, time.Second)
		require.Contains(t, gjson.GetBytes(wire, "input.0.content.0.text").String(), "<timezone>UTC</timezone>")
		require.Equal(t, "keep Asia/Shanghai", gjson.GetBytes(wire, "input.0.content.1.text").String())
		if turn == 1 {
			require.Equal(t, "resp_first", gjson.GetBytes(wire, "previous_response_id").String())
		}
		upstream.Send(`{"type":"response.completed","response":{"id":"resp_first","model":"gpt-5.1","usage":{"input_tokens":1,"output_tokens":1}}}`)
		_, err := readPassthroughLifecycleFrame(t, conn, time.Second)
		require.NoError(t, err)
	}
	conn.CloseNow()
	cancel()
	select {
	case <-serverErr:
	case <-time.After(2 * time.Second):
		t.Fatal("passthrough handler did not stop")
	}
}

func TestUpstreamClientTimezoneIngressActualFrame(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cfg := passthroughLifecycleConfig()
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	capture := &openAIWSCaptureConn{events: [][]byte{[]byte(`{"type":"response.completed","response":{"id":"resp_ingress","model":"gpt-5.1","usage":{"input_tokens":1,"output_tokens":1}}}`)}}
	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: capture})
	defer pool.Close()
	svc := newPassthroughLifecycleService(cfg, newStagedPassthroughConn())
	svc.openaiWSPool = pool
	account := passthroughLifecycleAccount()
	account.Extra["openai_apikey_responses_websockets_v2_mode"] = OpenAIWSIngressModeCtxPool
	account.Extra[UpstreamClientTimezoneModeKey] = "manual"
	account.Extra[UpstreamClientTimezoneKey] = "UTC"
	server, serverErr := startPassthroughLifecycleServer(t, ctx, svc, account)
	defer server.Close()
	conn, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	defer conn.CloseNow()
	payload := []byte(`{"type":"response.create","model":"gpt-5.1","input":[{"role":"user","internal_chat_message_metadata_passthrough":{"content_item_kinds":["environments.environment_context","text"]},"content":[{"type":"input_text","text":"<environment_context><timezone>Asia/Shanghai</timezone></environment_context>"},{"type":"input_text","text":"keep Asia/Shanghai"}]}]}`)
	require.NoError(t, conn.Write(ctx, coderws.MessageText, payload))
	_, err = readPassthroughLifecycleFrame(t, conn, time.Second)
	require.NoError(t, err)
	capture.mu.Lock()
	wire, marshalErr := json.Marshal(capture.lastWrite)
	capture.mu.Unlock()
	require.NoError(t, marshalErr)
	require.Contains(t, gjson.GetBytes(wire, "input.0.content.0.text").String(), "<timezone>UTC</timezone>")
	require.Equal(t, "keep Asia/Shanghai", gjson.GetBytes(wire, "input.0.content.1.text").String())
	conn.CloseNow()
	cancel()
	select {
	case <-serverErr:
	case <-time.After(2 * time.Second):
		t.Fatal("ingress handler did not stop")
	}
}
