package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Explicit shared state dependency for gateway tests; production uses Redis.
type excelBPSMemoryState struct {
	*stubGatewayCache
	mu       sync.Mutex
	items    map[string][]byte
	versions map[string]uint64
	revision uint64
}

func (m *excelBPSMemoryState) LoadBPSState(ctx context.Context, scope, kind string) ([]byte, uint64, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := scope + "\x00" + kind
	return bytes.Clone(m.items[key]), m.versions[key], nil
}
func (m *excelBPSMemoryState) SaveBPSState(ctx context.Context, scope, kind string, expected *uint64, raw []byte) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := scope + "\x00" + kind
	if expected != nil && m.versions[key] != *expected {
		return false, nil
	}
	if m.items == nil {
		m.items = map[string][]byte{}
		m.versions = map[string]uint64{}
	}
	m.revision++
	m.versions[key] = m.revision
	m.items[key] = bytes.Clone(raw)
	return true, nil
}
func newExcelBPSMemoryState() *excelBPSMemoryState {
	return &excelBPSMemoryState{stubGatewayCache: &stubGatewayCache{}}
}
func excelBPSTestService(upstream *httpUpstreamRecorder) *OpenAIGatewayService {
	s := openAIClientToolsTestService(upstream)
	s.cache = newExcelBPSMemoryState()
	return s
}

type excelBPSFidelityTransport struct {
	*httpUpstreamRecorder
	send  func(*http.Request, []byte, int) (*http.Response, error)
	calls int
	proxy string
}

func (u *excelBPSFidelityTransport) Do(req *http.Request, proxy string, _ int64, _ int) (*http.Response, error) {
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	_ = req.Body.Close()
	u.calls++
	u.proxy = proxy
	return u.send(req, raw, u.calls)
}
func excelBPSForwardForTest(t *testing.T, s *OpenAIGatewayService, a *Account, body []byte, thread string) (*OpenAIForwardResult, error, *httptest.ResponseRecorder) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(body))
	if thread != "" {
		c.Request.Header.Set("Thread-Id", thread)
	}
	result, err := s.Forward(context.Background(), c, a, body)
	return result, err, rec
}
func excelBPSImageBody(t *testing.T, tail string) ([]byte, []byte) {
	t.Helper()
	var imageBytes bytes.Buffer
	require.NoError(t, png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 2, 3))))
	url := "data:image/png;base64," + base64.StdEncoding.EncodeToString(imageBytes.Bytes())
	raw := []byte(`{"model":"gpt-6-astra","input":[{"role":"user","content":[{"type":"input_image","image_url":"` + url + `"}]}]` + tail + `}`)
	return raw, imageBytes.Bytes()
}
func TestExcelBPSForwardInlineImagesValidateUploadAndReuse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a := excelBPSAccount(t)
	a.Extra = map[string]any{CodexPrivacyModeKey: CodexPrivacyRandomized, codexFingerprintSeedExtraKey: testCodexFingerprintSeed}
	s := excelBPSTestService(nil)
	raw, pngBytes := excelBPSImageBody(t, "")
	uploads := 0
	responses := 0
	transport := &excelBPSFidelityTransport{httpUpstreamRecorder: new(httpUpstreamRecorder)}
	transport.send = func(req *http.Request, body []byte, _ int) (*http.Response, error) {
		require.Equal(t, "Bearer "+a.GetOpenAIAccessToken(), req.Header.Get("Authorization"))
		require.Equal(t, "workspace-test", req.Header.Get("Chatgpt-Account-Id"))
		require.True(t, HTTPUpstreamRedirectsDisabled(req.Context()))
		switch req.URL.Path {
		case "/basispoints/api/attachments":
			uploads++
			_, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
			require.NoError(t, err)
			part, err := multipart.NewReader(bytes.NewReader(body), params["boundary"]).NextPart()
			require.NoError(t, err)
			require.Equal(t, "file", part.FormName())
			decoded, err := io.ReadAll(part)
			require.NoError(t, err)
			require.Equal(t, pngBytes, decoded)
			return excelBPSMockResponse(200, `{"id":"file-image"}`), nil
		case "/basispoints/api/responses":
			responses++
			require.Contains(t, string(body), `"file_id":"file-image"`)
			require.NotContains(t, string(body), "base64,")
			return excelBPSMockResponse(200, excelBPSSuccessWire), nil
		default:
			return nil, fmt.Errorf("unexpected URL %s", req.URL)
		}
	}
	s.httpUpstream = transport
	for _, thread := range []string{"images-same", "images-same", "images-other"} {
		r, err, rec := excelBPSForwardForTest(t, s, a, raw, thread)
		require.NoError(t, err, rec.Body.String())
		require.Equal(t, 7, r.Usage.InputTokens)
	}
	require.Equal(t, 2, uploads)
	require.Equal(t, 3, responses)
	// A later unsupported item/control must fail before the first upload.
	invalid, _ := excelBPSImageBody(t, `,"previous_response_id":"resp_missing"`)
	_, err, rec := excelBPSForwardForTest(t, s, a, invalid, "")
	require.Error(t, err)
	require.Equal(t, 400, rec.Code)
	require.Equal(t, 5, transport.calls)
}
func TestExcelBPSForwardImageFailuresDoNotSendInference(t *testing.T) {
	for _, reply := range []string{`{"id":"not-file"}`, `not json`} {
		t.Run(reply, func(t *testing.T) {
			s := excelBPSTestService(nil)
			tr := &excelBPSFidelityTransport{httpUpstreamRecorder: new(httpUpstreamRecorder), send: func(*http.Request, []byte, int) (*http.Response, error) { return excelBPSMockResponse(200, reply), nil }}
			s.httpUpstream = tr
			raw, _ := excelBPSImageBody(t, "")
			r, err, rec := excelBPSForwardForTest(t, s, excelBPSAccount(t), raw, "")
			require.Error(t, err)
			require.Nil(t, r)
			require.Equal(t, 502, rec.Code)
			require.Contains(t, rec.Body.String(), "basispoints_attachment_failed")
			require.Equal(t, 1, tr.calls)
		})
	}
}
func TestExcelBPSForwardToolCorrectionSameAccountUsageAndExactBytes(t *testing.T) {
	for _, stream := range []bool{true, false} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			rawCode := "text('unchanged operation');\n"
			encode := func(v any) string { b, e := json.Marshal(v); require.NoError(t, e); return string(b) }
			response := func(id, marker, code string, input int) string {
				args := encode(map[string]any{"summary": marker, "code": code, "extended_summary": "{}"})
				return "data: " + encode(map[string]any{"type": "response.completed", "response": map[string]any{"id": id, "status": "completed", "model": "gpt-6-astra", "usage": map[string]any{"input_tokens": input, "output_tokens": 2}, "output": []any{map[string]any{"type": "function_call", "name": "run_officejs", "id": "fc_" + id, "call_id": id, "arguments": args}}}}) + "\n\n"
			}
			s := excelBPSTestService(nil)
			a := excelBPSAccount(t)
			a.Concurrency = 1
			tr := &excelBPSFidelityTransport{httpUpstreamRecorder: new(httpUpstreamRecorder)}
			tr.send = func(req *http.Request, raw []byte, n int) (*http.Response, error) {
				assert.Equal(t, "Bearer "+a.GetOpenAIAccessToken(), req.Header.Get("Authorization"))
				assert.Equal(t, "gpt-6-astra", gjson.GetBytes(raw, "model").String())
				if n == 1 {
					return excelBPSMockResponse(200, response("bad", "Run", rawCode, 10)), nil
				}
				assert.Contains(t, string(raw), "invalid_client_tool_transport")
				assert.Equal(t, "high", gjson.GetBytes(raw, "reasoning_effort").String())
				return excelBPSMockResponse(200, response("fixed", "codex2api.custom/functions.exec", "MUTATED", 20)), nil
			}
			s.httpUpstream = tr
			body := []byte(encode(map[string]any{"model": "gpt-6-astra", "stream": stream, "reasoning": map[string]any{"effort": "high"}, "input": "use exec", "tools": []any{map[string]any{"type": "custom", "name": "functions.exec"}}}))
			r, err, rec := excelBPSForwardForTest(t, s, a, body, "")
			require.NoError(t, err, rec.Body.String())
			require.Equal(t, 2, tr.calls)
			require.Equal(t, 30, r.Usage.InputTokens)
			require.Equal(t, 4, r.Usage.OutputTokens)
			require.Contains(t, rec.Body.String(), "unchanged operation")
			require.NotContains(t, rec.Body.String(), "MUTATED")
		})
	}
}
func TestExcelBPSForwardNoCorrectionForProviderErrorOrEOF(t *testing.T) {
	for _, wire := range []string{"data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"invalid_request_error\"}}}\n\n", "data: {\"type\":\"response.created\",\"response\":{\"id\":\"early\"}}\n\n"} {
		s := excelBPSTestService(nil)
		tr := &excelBPSFidelityTransport{httpUpstreamRecorder: new(httpUpstreamRecorder), send: func(*http.Request, []byte, int) (*http.Response, error) { return excelBPSMockResponse(200, wire), nil }}
		s.httpUpstream = tr
		_, err, _ := excelBPSForwardForTest(t, s, excelBPSAccount(t), []byte(`{"model":"gpt-6-astra","input":"hello","tools":[{"type":"custom","name":"functions.exec"}]}`), "")
		require.Error(t, err)
		require.Equal(t, 1, tr.calls)
	}
}

func TestExcelBPSForwardEncryptedReasoningRecoveryIsExactAndBounded(t *testing.T) {
	for _, code := range []string{"invalid_encrypted_content", "invalid_request_error"} {
		t.Run(code, func(t *testing.T) {
			a := excelBPSAccount(t)
			s := excelBPSTestService(nil)
			tr := &excelBPSFidelityTransport{httpUpstreamRecorder: new(httpUpstreamRecorder)}
			tr.send = func(req *http.Request, raw []byte, n int) (*http.Response, error) {
				if n == 1 {
					require.Contains(t, string(raw), "OPAQUE_REASONING")
					return excelBPSMockResponse(400, `{"error":{"code":"`+code+`"}}`), nil
				}
				require.NotContains(t, string(raw), "OPAQUE_REASONING")
				require.Contains(t, string(raw), "KEEP_SUMMARY")
				require.Contains(t, string(raw), "KEEP_TASK")
				require.Equal(t, "Bearer "+a.GetOpenAIAccessToken(), req.Header.Get("Authorization"))
				return excelBPSMockResponse(200, excelBPSSuccessWire), nil
			}
			s.httpUpstream = tr
			body := []byte(`{"model":"gpt-6-astra","input":[{"type":"reasoning","encrypted_content":"OPAQUE_REASONING","summary":[{"type":"summary_text","text":"KEEP_SUMMARY"}]},{"type":"message","role":"user","content":"KEEP_TASK"}]}`)
			_, err, rec := excelBPSForwardForTest(t, s, a, body, "")
			if code == "invalid_encrypted_content" {
				require.NoError(t, err, rec.Body.String())
				require.Equal(t, 2, tr.calls)
				require.Equal(t, "encrypted_reasoning_removed", rec.Header().Get("X-Sub2API-BPS-Recovery"))
			} else {
				require.Error(t, err)
				require.Equal(t, 1, tr.calls)
			}
		})
	}
	// Repeating the exact rejection does not start a third request.
	s := excelBPSTestService(nil)
	tr := &excelBPSFidelityTransport{httpUpstreamRecorder: new(httpUpstreamRecorder), send: func(*http.Request, []byte, int) (*http.Response, error) {
		return excelBPSMockResponse(400, `{"error":{"code":"invalid_encrypted_content"}}`), nil
	}}
	s.httpUpstream = tr
	_, err, _ := excelBPSForwardForTest(t, s, excelBPSAccount(t), []byte(`{"model":"gpt-6-astra","input":[{"type":"reasoning","encrypted_content":"opaque"},{"role":"user","content":"hello"}]}`), "")
	require.Error(t, err)
	require.Equal(t, 2, tr.calls)
}

func TestExcelBPSForwardCorrectionPreservesProviderFailureUsageAndModelEvidence(t *testing.T) {
	for _, scenario := range []string{"429", "503", "nested429", "transport", "timeout", "read_error", "read_timeout", "sse429", "sse503", "mismatch", "second_mismatch", "created_mismatch", "created_mismatch_missing", "ambiguous"} {
		t.Run(scenario, func(t *testing.T) {
			s := excelBPSTestService(nil)
			encode := func(v any) string { b, e := json.Marshal(v); require.NoError(t, e); return string(b) }
			response := func(id, model, marker, code string) string {
				return "data: " + encode(map[string]any{"type": "response.completed", "response": map[string]any{"id": id, "model": model, "status": "completed", "usage": map[string]any{"input_tokens": 10, "output_tokens": 2}, "output": []any{map[string]any{"type": "function_call", "id": "fc_" + id, "call_id": id, "name": "run_officejs", "arguments": encode(map[string]any{"summary": marker, "code": code, "extended_summary": "{}"})}}}}) + "\n\n"
			}
			tr := &excelBPSFidelityTransport{httpUpstreamRecorder: new(httpUpstreamRecorder)}
			tr.send = func(req *http.Request, body []byte, n int) (*http.Response, error) {
				if n == 1 {
					code := "text(1)"
					if scenario == "ambiguous" {
						code = `{"type":"custom","name":"functions.exec","input":"text(1)","args":"text(1)"}`
					}
					return excelBPSMockResponse(200, response("first", "gpt-6-astra", "Run", code)), nil
				}
				if scenario == "transport" {
					return nil, fmt.Errorf("private proxy 10.9.8.7:1234 unavailable")
				}
				if scenario == "timeout" {
					return nil, fmt.Errorf("private proxy 10.9.8.7:1234: %w", context.DeadlineExceeded)
				}
				if strings.HasPrefix(scenario, "read_") {
					readErr := errors.New("private proxy 10.9.8.7:1234 body read failed")
					if scenario == "read_timeout" {
						readErr = fmt.Errorf("private proxy 10.9.8.7:1234: %w", context.DeadlineExceeded)
					}
					response := excelBPSMockResponse(200, "")
					response.Body = &openAIStreamReadThenErrorCloser{reader: strings.NewReader("data: {\"type\":\"response.created\",\"response\":{\"id\":\"correction\",\"usage\":{\"input_tokens\":3,\"output_tokens\":1}}}\n\n"), err: readErr}
					return response, nil
				}
				if strings.HasPrefix(scenario, "sse") {
					status := 429
					if scenario == "sse503" {
						status = 503
					}
					return excelBPSMockResponse(200, "data: "+encode(map[string]any{"type": "response.failed", "response": map[string]any{"status": "failed", "usage": map[string]any{"input_tokens": 3, "output_tokens": 1}, "error": map[string]any{"status": status, "code": "server_error", "message": "private proxy 10.9.8.7:1234"}}})+"\n\n"), nil
				}
				if scenario == "second_mismatch" && n == 2 {
					return excelBPSMockResponse(200, response("still_invalid", "gpt-6-astra", "Run", "text(1)")), nil
				}
				if strings.HasPrefix(scenario, "created_mismatch") {
					model := "gpt-6-astra"
					if scenario == "created_mismatch_missing" {
						model = ""
					}
					return excelBPSMockResponse(200, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"fixed\",\"model\":\"gpt-6-luna\"}}\n\n"+response("fixed", model, "codex2api.custom/functions.exec", "text(1)")), nil
				}
				if scenario == "mismatch" || scenario == "second_mismatch" {
					return excelBPSMockResponse(200, response("fixed", "gpt-6-luna", "codex2api.custom/functions.exec", "text(1)")), nil
				}
				status := 429
				if scenario == "503" {
					status = 503
				}
				if scenario == "nested429" {
					return excelBPSMockResponse(status, `{"error":{"message":"provider private detail","usage":{"input_tokens":3,"output_tokens":1}}}`), nil
				}
				return excelBPSMockResponse(status, `{"usage":{"input_tokens":3,"output_tokens":1},"error":{"message":"provider private detail"}}`), nil
			}
			s.httpUpstream = tr
			result, err, rec := excelBPSForwardForTest(t, s, excelBPSAccount(t), []byte(`{"model":"gpt-6-astra","input":"execute","tools":[{"type":"custom","name":"functions.exec"}]}`), "")
			require.Error(t, err)
			require.NotNil(t, result)
			require.NotContains(t, rec.Body.String(), "provider private detail")
			require.NotContains(t, rec.Body.String(), "10.9.8.7")
			var local *OpenAILocalBridgeError
			var failover *UpstreamFailoverError
			if scenario == "ambiguous" {
				require.ErrorAs(t, err, &local)
				require.Equal(t, 1, tr.calls)
				require.NotContains(t, rec.Body.String(), "custom_tool_call")
			} else {
				require.False(t, errors.As(err, &local))
				expectedCalls := 2
				if scenario == "second_mismatch" {
					expectedCalls = 3
				}
				require.Equal(t, expectedCalls, tr.calls)
				require.ErrorAs(t, err, &failover)
				if strings.Contains(scenario, "mismatch") {
					require.Equal(t, "gpt-6-luna", result.UpstreamResponseModel)
					require.NotNil(t, result.UpstreamModelMismatchError())
					require.Equal(t, 10*expectedCalls, result.Usage.InputTokens)
				} else if scenario == "transport" || scenario == "timeout" {
					require.Equal(t, 10, result.Usage.InputTokens)
					require.Equal(t, 2, result.Usage.OutputTokens)
				} else {
					require.Equal(t, 13, result.Usage.InputTokens)
					require.Equal(t, 3, result.Usage.OutputTokens)
				}
			}
		})
	}
}

func TestExcelBPSForwardChargedHTTPRejectionPreservesUsageWithoutRecovery(t *testing.T) {
	for _, status := range []int{400, 429, 503} {
		for _, nested := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/nested=%t", status, nested), func(t *testing.T) {
				usage := `"usage":{"input_tokens":17,"output_tokens":3}`
				raw := `{"error":{"code":"invalid_encrypted_content"},` + usage + `}`
				if nested {
					raw = `{"error":{"code":"invalid_encrypted_content",` + usage + `}}`
				}
				s := excelBPSTestService(nil)
				tr := &excelBPSFidelityTransport{httpUpstreamRecorder: new(httpUpstreamRecorder), send: func(*http.Request, []byte, int) (*http.Response, error) {
					return excelBPSMockResponse(status, raw), nil
				}}
				s.httpUpstream = tr
				result, err, _ := excelBPSForwardForTest(t, s, excelBPSAccount(t), []byte(`{"model":"gpt-6-astra","input":[{"type":"reasoning","encrypted_content":"opaque"},{"role":"user","content":"hello"}]}`), "")
				require.Error(t, err)
				require.NotNil(t, result)
				require.Equal(t, 17, result.Usage.InputTokens)
				require.Equal(t, 3, result.Usage.OutputTokens)
				require.Equal(t, 1, tr.calls)
				require.False(t, result.SucceededForScheduling())
			})
		}
	}
}

func TestExcelBPSAttachmentPrivacyBypassIsInternalAndScoped(t *testing.T) {
	for _, scenario := range []string{"valid", "unmarked", "other_host", "other_path", "http", "native", "json"} {
		t.Run(scenario, func(t *testing.T) {
			account := excelBPSAccount(t)
			account.Extra = map[string]any{}
			account.Extra[CodexPrivacyModeKey] = CodexPrivacyRandomized
			account.Extra[codexFingerprintSeedExtraKey] = testCodexFingerprintSeed
			target := "https://bps.openai.com/basispoints/api/attachments"
			switch scenario {
			case "other_host":
				target = "https://example.com/basispoints/api/attachments"
			case "other_path":
				target = "https://bps.openai.com/basispoints/api/responses"
			case "http":
				target = "http://bps.openai.com/basispoints/api/attachments"
			case "native":
				account = privacyTestAccount(AccountTypeOAuth)
			}
			ctx := context.Background()
			if scenario != "unmarked" {
				ctx = context.WithValue(ctx, excelBPSAttachmentRequestKey{}, true)
			}
			req, err := http.NewRequestWithContext(ctx, "POST", target, io.NopCloser(strings.NewReader("unaltered generated multipart bytes")))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "multipart/form-data; boundary=generated")
			if scenario == "json" {
				req.Header.Set("Content-Type", "application/json")
			}
			req.Header.Set("X-Codex-Installation-Id", privacyTestUUID)
			projected, err := applyCodexPrivacyBeforeSend(req, account)
			if scenario != "valid" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			raw, err := io.ReadAll(projected.Body)
			require.NoError(t, err)
			require.Equal(t, "unaltered generated multipart bytes", string(raw))
			require.NotEqual(t, privacyTestUUID, projected.Header.Get("X-Codex-Installation-Id"))
		})
	}
}

func TestExcelBPSLocalPreflightErrorsDoNotBecomeProviderFailures(t *testing.T) {
	for _, scenario := range []string{"compact", "policy", "invalid_timezone_context", "timezone_probe"} {
		t.Run(scenario, func(t *testing.T) {
			s := excelBPSTestService(nil)
			account := excelBPSAccount(t)
			account.Extra = map[string]any{}
			body := []byte(`{"model":"gpt-6-astra","input":"hello"}`)
			target, status := "/v1/responses", http.StatusBadRequest
			switch scenario {
			case "compact":
				target = "/v1/responses/compact"
			case "policy":
				account.Extra["codex_cli_only"] = true
				status = http.StatusForbidden
			case "invalid_timezone_context":
				account.Extra[UpstreamClientTimezoneModeKey] = "manual"
				account.Extra[UpstreamClientTimezoneKey] = "America/New_York"
				body = timezoneTestBody(t, "<environment_context><timezone>UTC</timezone>", true)
			case "timezone_probe":
				old := defaultUpstreamTimezoneResolver
				resolver := newUpstreamTimezoneResolver()
				resolver.trace = func(context.Context, string) (upstreamTimezoneExit, error) {
					return upstreamTimezoneExit{}, errors.New("private proxy trace detail")
				}
				defaultUpstreamTimezoneResolver = resolver
				t.Cleanup(func() { defaultUpstreamTimezoneResolver = old })
				account.Extra[UpstreamClientTimezoneModeKey] = "auto"
				body = timezoneTestBody(t, "<environment_context><timezone>UTC</timezone></environment_context>", true)
				status = http.StatusBadGateway
			}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", target, bytes.NewReader(body))
			result, err := s.Forward(context.Background(), c, account, body)
			require.Nil(t, result)
			var local *OpenAILocalBridgeError
			require.ErrorAs(t, err, &local)
			require.Equal(t, status, rec.Code)
			require.NotContains(t, rec.Body.String(), "private proxy")
		})
	}
}

func TestExcelBPSCorrectionProviderFailureAfterVisibleOutputDoesNotReplay(t *testing.T) {
	for _, status := range []int{429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			s := excelBPSTestService(nil)
			tr := &excelBPSFidelityTransport{httpUpstreamRecorder: new(httpUpstreamRecorder)}
			tr.send = func(_ *http.Request, _ []byte, n int) (*http.Response, error) {
				if n == 1 {
					return excelBPSMockResponse(200, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"original\",\"model\":\"gpt-6-astra\"}}\n\n"+
						"data: {\"type\":\"response.output_text.delta\",\"delta\":\"already visible\"}\n\n"+
						"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"original\",\"status\":\"completed\",\"model\":\"gpt-6-astra\",\"usage\":{\"input_tokens\":10,\"output_tokens\":2},\"output\":[{\"type\":\"function_call\",\"name\":\"run_officejs\",\"id\":\"fc_old\",\"call_id\":\"old\",\"arguments\":\"{\\\"summary\\\":\\\"Run\\\",\\\"code\\\":\\\"text(1)\\\"}\"}]}}\n\n"), nil
				}
				return excelBPSMockResponse(status, `{"usage":{"input_tokens":3,"output_tokens":1},"error":{"message":"private provider text"}}`), nil
			}
			s.httpUpstream = tr
			result, err, rec := excelBPSForwardForTest(t, s, excelBPSAccount(t), []byte(`{"model":"gpt-6-astra","stream":true,"input":"execute","tools":[{"type":"custom","name":"functions.exec"}]}`), "")
			require.NotNil(t, result)
			var failover *UpstreamFailoverError
			require.False(t, errors.As(err, &failover))
			require.Equal(t, 2, tr.calls)
			require.Equal(t, 13, result.Usage.InputTokens)
			require.Equal(t, 3, result.Usage.OutputTokens)
			require.Contains(t, rec.Body.String(), "already visible")
			require.Contains(t, rec.Body.String(), "response.failed")
			require.NotContains(t, rec.Body.String(), "custom_tool_call")
			require.NotContains(t, rec.Body.String(), "private provider text")
		})
	}
}
