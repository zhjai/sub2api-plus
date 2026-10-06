package service

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/prism"
	"github.com/gin-gonic/gin"
)

type prismTestStore struct {
	GatewayCache
	states map[string][]byte
}

func (s *prismTestStore) GetPrismResponse(_ context.Context, key string) ([]byte, error) {
	return s.states[key], nil
}
func (s *prismTestStore) PutPrismResponse(_ context.Context, key string, b []byte, _ time.Duration) error {
	s.states[key] = b
	return nil
}
func prismTestContext(body string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body))
	c.Set("api_key", &APIKey{ID: 7})
	return c, w
}

func TestPrismResponseScopeIsolation(t *testing.T) {
	a := &Account{ID: 1, Credentials: map[string]any{"cookie": "one"}}
	base := prismResponseScope(7, a)
	if prismResponseScope(8, a) == base {
		t.Fatal("key collision")
	}
	b := *a
	b.ID = 2
	if prismResponseScope(7, &b) == base {
		t.Fatal("account collision")
	}
	b = *a
	b.Credentials = map[string]any{"cookie": "two"}
	if prismResponseScope(7, &b) == base {
		t.Fatal("credential collision")
	}
	if strings.Contains(base, "one") {
		t.Fatal("credential leaked")
	}
}
func TestPrismContinuationReferenceWrongOwnerRejected(t *testing.T) {
	a := &Account{ID: 1, Credentials: map[string]any{"cookie": "one"}}
	state, _ := json.Marshal(prismResponseState{Items: []json.RawMessage{json.RawMessage(`{"role":"user","content":"private"}`)}})
	store := &prismTestStore{states: map[string][]byte{prismResponseScope(8, a) + "resp_prism_old": state}}
	s := &OpenAIGatewayService{cache: store}
	body := `{"model":"m","input":"next","previous_response_id":"resp_prism_old"}`
	c, w := prismTestContext(body)
	_, err := s.forwardPrismResponses(context.Background(), c, a, []byte(body))
	if err == nil || w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_previous_response_id") {
		t.Fatalf("status=%d body=%s err=%v", w.Code, w.Body.String(), err)
	}
}

func TestPrismResponseScopeSurvivesVerifiedTokenRotation(t *testing.T) {
	a := &Account{ID: 1, Credentials: map[string]any{"prism_verified_identity": "verified-owner", "access_token": "old"}}
	base := prismResponseScope(7, a)
	a.Credentials["access_token"] = "new"
	a.Credentials["prism_verified_at"] = "later"
	a.Credentials["model_mapping"] = map[string]any{"alias": "model"}
	if prismResponseScope(7, a) != base {
		t.Fatal("same-owner token rotation discarded continuation")
	}
	a.Credentials["prism_verified_identity"] = "another-owner"
	if prismResponseScope(7, a) == base {
		t.Fatal("different owner reused continuation")
	}
}
func TestPrismCompactDoesNotCallUpstream(t *testing.T) {
	s := &OpenAIGatewayService{}
	for _, header := range []bool{false, true} {
		body := `{"model":"m","input":"hello","compact":true}`
		if header {
			body = `{"model":"m","input":"hello"}`
		}
		c, w := prismTestContext(body)
		if header {
			c.Request.Header.Set("X-Codex-Request-Kind", "compact")
		}
		_, err := s.forwardPrismResponses(context.Background(), c, &Account{ID: 1}, []byte(body))
		if err == nil || w.Code != 400 {
			t.Fatalf("status=%d err=%v", w.Code, err)
		}
	}
}
func TestPrismResponseStreamToolTerminal(t *testing.T) {
	c, w := prismTestContext("")
	writer := &prismEventWriter{c: c}
	writer.begin()
	output := []json.RawMessage{json.RawMessage(`{"id":"fc_1","type":"custom_tool_call","name":"exec","call_id":"call_1","input":"text(1)","status":"completed"}`)}
	if err := writer.completed(prismResponseObject("resp_prism_1", "m", output, nil), output); err != nil {
		t.Fatal(err)
	}
	text := w.Body.String()
	for _, want := range []string{"response.created", "response.output_item.added", "response.custom_tool_call_input.delta", "response.custom_tool_call_input.done", "response.output_item.done", "response.completed", `"usage":null`} {
		if !strings.Contains(text, want) {
			t.Fatal(want)
		}
	}
	if strings.LastIndex(text, "event: response.completed") < strings.LastIndex(text, "event: response.output_item.done") {
		t.Fatal("terminal ordering")
	}
}
func TestPrismStreamFailureCannotComplete(t *testing.T) {
	c, w := prismTestContext("")
	writer := &prismEventWriter{c: c}
	writer.begin()
	_ = writer.failure("resp_prism_1", "m")
	if !strings.Contains(w.Body.String(), "response.failed") || strings.Contains(w.Body.String(), "response.completed") {
		t.Fatal(w.Body.String())
	}
}
func TestPrismChatRejectsGenericTools(t *testing.T) {
	if _, err := prismChatToResponses([]byte(`{"model":"m","messages":[{"role":"user","content":"x"}],"tools":[{"type":"function","function":{"name":"f"}}]}`)); err == nil {
		t.Fatal("generic tools accepted")
	}
	b, err := prismChatToResponses([]byte(`{"model":"m","messages":[{"role":"user","content":"old"},{"role":"assistant","content":"answer"},{"role":"user","content":"next"}]}`))
	if err != nil || !strings.Contains(string(b), "old") || !strings.Contains(string(b), "answer") {
		t.Fatalf("%s %v", b, err)
	}
}
func TestPrismUsageIsNeverEstimated(t *testing.T) {
	r := prismResponseObject("r", "m", nil, nil)
	if r["usage"] != nil {
		t.Fatal("fabricated usage")
	}
	r = prismResponseObject("r", "m", nil, &prism.Usage{InputTokens: 3, OutputTokens: 4, TotalTokens: 7})
	if r["usage"].(map[string]any)["total_tokens"] != 7 {
		t.Fatal(r)
	}
}

func TestPrismProgressAppendOnlyAndTerminal(t *testing.T) {
	c, w := prismTestContext("")
	writer := &prismEventWriter{c: c}
	writer.begin()
	p := &prismProgress{w: writer, id: "resp_prism_x", model: "m"}
	for _, text := range []string{"hel", "hello", "hello"} {
		if err := p.observe(&prism.StatusResponse{Progress: []prism.LiveProgressEvent{{Type: "agent_message", LineIndex: 1, Text: text}, {Type: "agent_reasoning", LineIndex: 2, Text: "private reasoning"}}}); err != nil {
			t.Fatal(err)
		}
	}
	before := w.Body.String()
	if strings.Contains(before, "response.completed") || strings.Contains(before, "private reasoning") || strings.Count(before, `"delta":"lo"`) != 1 {
		t.Fatal(before)
	}
	if err := p.finish(); err != nil {
		t.Fatal(err)
	}
	writer.progressStarted = true
	writer.outputOffset = len(p.items)
	if err := writer.completed(prismResponseObject(p.id, p.model, p.items, nil), nil); err != nil {
		t.Fatal(err)
	}
	body := w.Body.String()
	if strings.Count(body, "event: response.created") != 1 || strings.Count(body, "event: response.completed") != 1 || strings.LastIndex(body, "event: response.output_text.done") > strings.LastIndex(body, "event: response.completed") {
		t.Fatal(body)
	}
}

func TestPrismProgressRejectsRewriteResetAndTools(t *testing.T) {
	for _, bad := range []*prism.StatusResponse{{Reset: true}, {Progress: []prism.LiveProgressEvent{{Type: "agent_message", LineIndex: 1, Text: "changed"}}}, {Progress: []prism.LiveProgressEvent{{Type: "agent_message", LineIndex: 1, Text: "hello```codex-exec\nsecret"}}}} {
		c, w := prismTestContext("")
		p := &prismProgress{w: &prismEventWriter{c: c}, id: "r", model: "m"}
		if err := p.observe(&prism.StatusResponse{Progress: []prism.LiveProgressEvent{{Type: "agent_message", LineIndex: 1, Text: "hello"}}}); err != nil {
			t.Fatal(err)
		}
		if err := p.observe(bad); err == nil {
			t.Fatal("accepted invalid progress")
		}
		if strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "response.completed") {
			t.Fatal(w.Body.String())
		}
	}
}

func TestPrismProgressHoldsSplitToolFence(t *testing.T) {
	c, w := prismTestContext("")
	p := &prismProgress{w: &prismEventWriter{c: c}, id: "r", model: "m"}
	if err := p.observe(&prism.StatusResponse{Progress: []prism.LiveProgressEvent{{Type: "agent_message", LineIndex: 1, Text: "hello```code"}}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(w.Body.String(), "```code") {
		t.Fatal("partial tool protocol escaped")
	}
	if err := p.observe(&prism.StatusResponse{Progress: []prism.LiveProgressEvent{{Type: "agent_message", LineIndex: 1, Text: "hello```codex-exec\ncmd"}}}); err == nil {
		t.Fatal("tool fence accepted")
	}
}

func TestPrismChatProgressDefersFinishUntilTerminal(t *testing.T) {
	c, w := prismTestContext("")
	writer := &prismEventWriter{c: c, chat: true}
	p := &prismProgress{w: writer, id: "r", model: "m"}
	if err := p.observe(&prism.StatusResponse{Text: "first"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(w.Body.String(), "[DONE]") || strings.Contains(w.Body.String(), `"finish_reason":"stop"`) {
		t.Fatal("premature finish")
	}
	if err := p.observe(&prism.StatusResponse{Done: true, Text: "first last"}); err != nil {
		t.Fatal(err)
	}
	if err := prismWriteChat(c, writer, "r", "m", "", nil); err != nil {
		t.Fatal(err)
	}
	if strings.Count(w.Body.String(), `"content":"first"`) != 1 || strings.Count(w.Body.String(), `"content":" last"`) != 1 || strings.Count(w.Body.String(), "[DONE]") != 1 {
		t.Fatal(w.Body.String())
	}
}
