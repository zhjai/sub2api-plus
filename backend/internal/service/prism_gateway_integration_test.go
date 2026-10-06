package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	bridgeconfig "github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/creds"
	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/httpc"
	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/prism"
)

type prismHTTPFixture struct {
	mu     sync.Mutex
	starts []string
	stops  int
	output string
	fail   bool
	cancel context.CancelFunc
}

func (f *prismHTTPFixture) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case prism.PathProjects:
		fmt.Fprint(w, `{"uuid":"project-one"}`)
	case prism.PathSandboxNew:
		fmt.Fprint(w, `{"url":"https://prism.openai.com/s/sandboxes/proxy","token":"fixture-sandbox"}`)
	case "/api/projects/project-one/sandbox/resources-token":
		fmt.Fprint(w, `{"access_token":"fixture-resource","resources_base_url":"https://prism.openai.com/s/sandbox-resources"}`)
	case prism.PathYSweetToken:
		fmt.Fprint(w, `{"token":"fixture-y","url":"wss://example.invalid","docId":"project-one"}`)
	case "/s/sandboxes/proxy/resources-token", "/s/sandboxes/proxy/token":
		fmt.Fprint(w, `{}`)
	case "/s/sandboxes/proxy/wait-for-sync":
		w.WriteHeader(404)
	case "/":
		fmt.Fprint(w, "0:{\"a\":\"$@1\",\"f\":\"\",\"b\":\"x\"}\n1:\"cdx_fixture\"\n")
	case prism.PathResponseStop:
		f.mu.Lock()
		f.stops++
		f.mu.Unlock()
		fmt.Fprint(w, `{}`)
	case prism.PathResponseStatus:
		f.mu.Lock()
		cancel := f.cancel
		f.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		fmt.Fprint(w, `{"status":"pending","request_id":"req_fixture","turn_state":{"opaque":"two"}}`)
	case prism.PathResponseStart:
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.starts = append(f.starts, string(body))
		cancel := f.cancel
		output, fail := f.output, f.fail
		f.mu.Unlock()
		if cancel != nil {
			fmt.Fprint(w, `{"status":"started","request_id":"req_fixture","turn_state":{"opaque":"one"}}`)
			return
		}
		status := "success"
		payload := map[string]any{"id": "upstream_response", "conversationId": "cdx_fixture", "usage": map[string]any{"input_tokens": 11, "output_tokens": 7, "total_tokens": 18}, "output": []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": output}}}}}
		if fail {
			status = "error"
			payload = map[string]any{"reason": "fixture_failure", "message": "SECRET_FIXTURE_ERROR"}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "completed", "request_id": "req_fixture", "usage": map[string]any{"input_tokens": 11, "output_tokens": 7, "total_tokens": 18}, "response": map[string]any{"status": status, "payload": payload}})
	default:
		http.NotFound(w, r)
	}
}

func newPrismForwardFixture(t *testing.T, f *prismHTTPFixture) (*OpenAIGatewayService, *prismTestStore, *Account) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(server.Close)
	cfg := bridgeconfig.Default().Upstream
	cfg.BaseURL = server.URL
	hc, err := httpc.New(cfg, httpc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(hc.CloseIdle)
	pc := prism.NewConfigured(hc, cfg)
	store := &prismTestStore{states: map[string][]byte{}}
	s := &OpenAIGatewayService{cache: store, openaiWSStateStore: NewOpenAIWSStateStore(nil), prismRuntime: &prismGatewayRuntime{
		resolveModel: func(_ context.Context, _ *Account, m, e string) (string, string, error) { return m, e, nil },
		principal: func(context.Context, *Account) (*prism.Client, prism.Principal, func(), error) {
			return pc, prism.Principal{Client: hc, Cred: &creds.Credential{UserID: "fixture-user"}}, func() {}, nil
		},
	}}
	a := &Account{ID: 91, Platform: "prism", Credentials: map[string]any{"prism_verified_identity": "verified-fixture-owner", "access_token": "fake-old"}}
	return s, store, a
}

func TestPrismForwardHTTPToolContinuationSameOwnerRefresh(t *testing.T) {
	f := &prismHTTPFixture{output: "```codex-exec\ntext(await tools.exec_command({cmd: 'pwd'}));\n```"}
	s, store, a := newPrismForwardFixture(t, f)
	body := `{"model":"fixture-model","input":"remember-original-request","tools":[{"type":"custom","name":"exec"}]}`
	c, w := prismTestContext(body)
	result, err := s.forwardPrismResponses(context.Background(), c, a, []byte(body))
	if err != nil || w.Code != 200 || result.Usage.InputTokens != 11 || result.Usage.OutputTokens != 7 || !result.ResponsesToolCallForwarded {
		t.Fatalf("result=%+v err=%v body=%s", result, err, w.Body.String())
	}
	var response struct {
		ID     string `json:"id"`
		Output []struct {
			CallID string `json:"call_id"`
		} `json:"output"`
	}
	if json.Unmarshal(w.Body.Bytes(), &response) != nil || len(response.Output) != 1 {
		t.Fatal(w.Body.String())
	}
	a.Credentials["access_token"] = "fake-rotated"
	a.Credentials["model_mapping"] = map[string]any{"alias": "fixture-model"}
	f.mu.Lock()
	f.output = "followup acknowledged"
	f.mu.Unlock()
	b, _ := json.Marshal(map[string]any{"model": "fixture-model", "previous_response_id": response.ID, "input": []any{map[string]any{"type": "custom_tool_call_output", "call_id": response.Output[0].CallID, "output": "TOOL_RESULT_FULL"}}})
	c, w = prismTestContext(string(b))
	result, err = s.forwardPrismResponses(context.Background(), c, a, b)
	if err != nil || result.ResponsesStatus != "completed" || !strings.Contains(w.Body.String(), "followup acknowledged") {
		t.Fatalf("result=%+v err=%v body=%s", result, err, w.Body.String())
	}
	if len(store.states) != 2 || len(f.starts) != 2 {
		t.Fatalf("states=%d starts=%d", len(store.states), len(f.starts))
	}
	for _, want := range []string{"remember-original-request", "TOOL_RESULT_FULL", response.Output[0].CallID, "pwd"} {
		if !strings.Contains(f.starts[1], want) {
			t.Fatalf("continuation lost %s", want)
		}
	}
}

func TestPrismForwardHTTPBusinessFailureNotPersisted(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			f := &prismHTTPFixture{fail: true}
			s, store, a := newPrismForwardFixture(t, f)
			body := fmt.Sprintf(`{"model":"m","input":"x","stream":%t}`, stream)
			c, w := prismTestContext(body)
			result, err := s.forwardPrismResponses(context.Background(), c, a, []byte(body))
			if err == nil || result.ResponsesStatus != "failed" || len(store.states) != 0 || len(f.starts) != 1 || strings.Contains(w.Body.String(), "response.completed") || strings.Contains(w.Body.String(), "SECRET_FIXTURE_ERROR") {
				t.Fatalf("result=%+v err=%v body=%s", result, err, w.Body.String())
			}
		})
	}
}

func TestPrismForwardHTTPCancellationStopsAcceptedRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &prismHTTPFixture{cancel: cancel}
	s, store, a := newPrismForwardFixture(t, f)
	body := `{"model":"m","input":"x","stream":true}`
	c, w := prismTestContext(body)
	c.Request = c.Request.WithContext(ctx)
	result, err := s.forwardPrismResponses(ctx, c, a, []byte(body))
	if err == nil || !result.ClientDisconnect || len(store.states) != 0 || strings.Contains(w.Body.String(), "response.completed") {
		t.Fatalf("result=%+v err=%v body=%s", result, err, w.Body.String())
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stops != 1 || len(f.starts) != 1 {
		t.Fatalf("starts=%d stops=%d", len(f.starts), f.stops)
	}
}

func TestPrismForwardHTTPTextResponsesAndChat(t *testing.T) {
	for _, chat := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("chat=%t/stream=%t", chat, stream), func(t *testing.T) {
				f := &prismHTTPFixture{output: "exact-final-answer"}
				s, store, a := newPrismForwardFixture(t, f)
				body := fmt.Sprintf(`{"model":"m","input":"x","stream":%t}`, stream)
				if chat {
					body = fmt.Sprintf(`{"model":"m","messages":[{"role":"user","content":"x"}],"stream":%t}`, stream)
				}
				c, w := prismTestContext(body)
				var result *OpenAIForwardResult
				var err error
				if chat {
					result, err = s.forwardPrismChat(context.Background(), c, a, []byte(body))
				} else {
					result, err = s.forwardPrismResponses(context.Background(), c, a, []byte(body))
				}
				if err != nil || result.ResponsesStatus != "completed" || result.Usage.InputTokens != 11 || !strings.Contains(w.Body.String(), "exact-final-answer") {
					t.Fatalf("result=%+v err=%v body=%s", result, err, w.Body.String())
				}
				if chat && len(store.states) != 0 {
					t.Fatal("chat should not persist references")
				}
				if stream && chat && strings.Count(w.Body.String(), "[DONE]") != 1 {
					t.Fatal(w.Body.String())
				}
				if stream && !chat && strings.Count(w.Body.String(), "event: response.completed") != 1 {
					t.Fatal(w.Body.String())
				}
			})
		}
	}
}
