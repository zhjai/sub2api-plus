package prism

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestGenericStartOnlyAttachesProgress(t *testing.T) {
	for _, progress := range []bool{false, true} {
		body := map[string]any{
			"metadata": map[string]any{"request_id": "req-generic", "completed": true},
			"input":    map[string]any{"text": "echoed prompt is not an answer"}, "error": "unrelated metadata",
		}
		if progress {
			body["codex_live_progress"] = map[string]any{"eventPreviews": []any{
				map[string]any{"line_index": 1, "payload_type": "agent_message", "raw": map[string]any{"payload": map[string]any{"message": "working"}}},
			}}
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(body)
		}))
		client := newTestClient(t, server.URL)
		response, err := client.StartResponse(context.Background(), Principal{}, &StartRequest{})
		server.Close()
		if err != nil || response.RequestID != "req-generic" {
			t.Fatalf("generic start lost ID: %+v %v", response, err)
		}
		if !progress && response.Initial != nil {
			t.Fatal("generic echoed prompt created initial status")
		}
		if progress && (response.Initial == nil || response.Initial.Done || response.Initial.Fail || response.Initial.Text != "" || len(response.Initial.Progress) != 1) {
			t.Fatalf("generic metadata treated as terminal content: %+v", response.Initial)
		}
	}
}

func TestLiveProgressEnvelopeAndFallback(t *testing.T) {
	progress := `{"eventPreviews":[
		{"line_index":1,"payload_type":"agent_reasoning","raw":{"payload":{"text":"thinking"}}},
		{"line_index":2,"payload_type":"agent_message","raw":"{\"payload\":{\"message\":\"working\"}}"},
		{"line_index":3,"payload_type":"agent_reasoning","payload":{"text":"direct"}},
		{"line_index":4,"payload_type":"agent_message","message":"entry"},
		{"line_index":5,"payload_type":"unknown","payload":{"text":"ignore"}},
		{"line_index":-1,"payload_type":"agent_reasoning","payload":{"text":"ignore"}},
		{"line_index":"6","payload_type":"agent_reasoning","payload":{"text":"ignore"}},
		{"line_index":7.1,"payload_type":"agent_reasoning","payload":{"text":"ignore"}},
		{"line_index":8,"payload_type":"agent_message","raw":"invalid"},null
	],"reasoningSummaries":[{"line_index":9,"text":"summary"},{"line_index":10,"text":12}]}`
	want := []LiveProgressEvent{{"agent_reasoning", 1, "thinking"}, {"agent_message", 2, "working"}, {"agent_reasoning", 3, "direct"}, {"agent_message", 4, "entry"}, {"agent_reasoning", 9, "summary"}}
	c := New(nil, UpstreamOptions{}, SchemaOptions{RespTextKeys: []string{"text"}, RespStatusKeys: []string{"status"}, StatusDone: []string{"completed"}})
	for _, prefix := range []string{
		`"request_id":"req","conversation_id":"conv","turn_state":{"cursor":3},`,
		// Non-string message forces the tolerant envelope branch.
		`"request_id":"req","message":{},"conversation_id":"conv","turn_state":{"cursor":3},`,
		``,
	} {
		st, err := c.ParseStatusPayload([]byte(`{`+prefix+`"status":"pending","codex_live_progress":`+progress+`}`), "req", "")
		if err != nil || !reflect.DeepEqual(st.Progress, want) {
			t.Fatalf("prefix=%s: status=%+v err=%v", prefix, st, err)
		}
		if st.Text != "" || st.Delta != "" || st.Reasoning != "" || st.Done || st.Fail {
			t.Fatalf("progress contaminated status: %+v", st)
		}
		if prefix != "" && (st.ConversationID != "conv" || string(st.TurnState) != `{"cursor":3}`) {
			t.Fatalf("lost envelope state: %+v", st)
		}
	}
}

func TestLiveProgressPreservesTerminalPayload(t *testing.T) {
	c := New(nil, UpstreamOptions{}, SchemaOptions{})
	for _, failed := range []bool{false, true} {
		payload := map[string]any{"status": "completed", "request_id": "req", "codex_live_progress": map[string]any{
			"eventPreviews": []any{map[string]any{"line_index": 1, "payload_type": "agent_message", "payload": map[string]any{"message": "not the answer"}}},
		}}
		if failed {
			payload["response"] = map[string]any{"status": "error", "payload": map[string]any{"reason": "bad", "message": "failed"}}
		} else {
			payload["response"] = map[string]any{"status": "success", "payload": map[string]any{"id": "resp", "conversationId": "conv", "output": []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "21"}}}}}}
		}
		b, _ := json.Marshal(payload)
		st, err := c.ParseStatusPayload(b, "req", "")
		if err != nil || !st.Done || st.Fail != failed || len(st.Progress) != 1 {
			t.Fatalf("status=%+v err=%v", st, err)
		}
		if failed && (st.Error != "failed" || st.ErrorReason != "bad" || st.Text != "") {
			t.Fatalf("error changed: %+v", st)
		}
		if !failed && (st.Text != "21" || st.Delta != "21" || st.ResponseID != "resp" || st.ConversationID != "conv") {
			t.Fatalf("answer changed: %+v", st)
		}
	}
}
