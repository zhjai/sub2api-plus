package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/prism"
	"github.com/gin-gonic/gin"
)

type prismEventWriter struct {
	c               *gin.Context
	sequence        int
	chat            bool
	progressStarted bool
	outputOffset    int
}

func (w *prismEventWriter) begin() {
	w.c.Header("Content-Type", "text/event-stream")
	w.c.Header("Cache-Control", "no-cache")
	w.c.Header("X-Accel-Buffering", "no")
}
func (w *prismEventWriter) heartbeat() error {
	_, err := w.c.Writer.WriteString(": prism pending\n\n")
	if err == nil {
		w.c.Writer.Flush()
	}
	return err
}
func (w *prismEventWriter) data(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w.c.Writer, "data: %s\n\n", b)
	if err == nil {
		w.c.Writer.Flush()
	}
	return err
}
func (w *prismEventWriter) event(typ string, v map[string]any) error {
	v["type"] = typ
	v["sequence_number"] = w.sequence
	w.sequence++
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w.c.Writer, "event: %s\ndata: %s\n\n", typ, b)
	if err == nil {
		w.c.Writer.Flush()
	}
	return err
}
func (w *prismEventWriter) failure(id, model string) error {
	e := map[string]any{"type": "server_error", "code": "upstream_error", "message": "Prism did not produce a completed response"}
	if w.chat {
		return w.data(map[string]any{"error": e})
	}
	r := prismResponseObject(id, model, []json.RawMessage{}, nil)
	r["status"] = "failed"
	r["error"] = e
	return w.event("response.failed", map[string]any{"response": r})
}

func prismResponseObject(id, model string, output []json.RawMessage, usage *prism.Usage) map[string]any {
	var u any
	if usage != nil {
		u = map[string]any{"input_tokens": usage.InputTokens, "output_tokens": usage.OutputTokens, "total_tokens": usage.TotalTokens, "input_tokens_details": map[string]any{"cached_tokens": 0}, "output_tokens_details": map[string]any{"reasoning_tokens": usage.ReasoningTokens}}
	}
	return map[string]any{"id": id, "object": "response", "created_at": time.Now().Unix(), "status": "completed", "error": nil, "incomplete_details": nil, "model": model, "output": output, "usage": u, "metadata": map[string]any{"prism_tool_mode": "emulated_bridge", "usage_source": map[bool]string{true: "upstream", false: "unavailable"}[usage != nil]}}
}

func (w *prismEventWriter) completed(response map[string]any, output []json.RawMessage) error {
	initial := make(map[string]any, len(response))
	for k, v := range response {
		initial[k] = v
	}
	initial["status"] = "in_progress"
	initial["output"] = []any{}
	initial["usage"] = nil
	if !w.progressStarted {
		if err := w.event("response.created", map[string]any{"response": initial}); err != nil {
			return err
		}
		if err := w.event("response.in_progress", map[string]any{"response": initial}); err != nil {
			return err
		}
	}
	for i, raw := range output {
		i += w.outputOffset
		var item map[string]any
		if err := json.Unmarshal(raw, &item); err != nil {
			return err
		}
		id, _ := item["id"].(string)
		typ, _ := item["type"].(string)
		added := make(map[string]any, len(item))
		for k, v := range item {
			added[k] = v
		}
		added["status"] = "in_progress"
		switch typ {
		case "message":
			added["content"] = []any{}
		case "function_call":
			added["arguments"] = ""
		case "custom_tool_call":
			added["input"] = ""
		}
		if err := w.event("response.output_item.added", map[string]any{"output_index": i, "item": added}); err != nil {
			return err
		}
		switch typ {
		case "message":
			contents, _ := item["content"].([]any)
			for ci, p := range contents {
				part, _ := p.(map[string]any)
				text, _ := part["text"].(string)
				if err := w.event("response.content_part.added", map[string]any{"output_index": i, "item_id": id, "content_index": ci, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}}); err != nil {
					return err
				}
				if err := w.event("response.output_text.delta", map[string]any{"output_index": i, "item_id": id, "content_index": ci, "delta": text}); err != nil {
					return err
				}
				if err := w.event("response.output_text.done", map[string]any{"output_index": i, "item_id": id, "content_index": ci, "text": text}); err != nil {
					return err
				}
				if err := w.event("response.content_part.done", map[string]any{"output_index": i, "item_id": id, "content_index": ci, "part": part}); err != nil {
					return err
				}
			}
		case "function_call", "custom_tool_call":
			field, event := "arguments", "response.function_call_arguments"
			if typ == "custom_tool_call" {
				field, event = "input", "response.custom_tool_call_input"
			}
			if err := w.event(event+".delta", map[string]any{"output_index": i, "item_id": id, "delta": item[field]}); err != nil {
				return err
			}
			if err := w.event(event+".done", map[string]any{"output_index": i, "item_id": id, field: item[field]}); err != nil {
				return err
			}
		}
		if err := w.event("response.output_item.done", map[string]any{"output_index": i, "item": item}); err != nil {
			return err
		}
	}
	return w.event("response.completed", map[string]any{"response": response})
}

func prismChatToResponses(body []byte) ([]byte, error) {
	var req map[string]json.RawMessage
	if json.Unmarshal(body, &req) != nil {
		return nil, errors.New("invalid Chat Completions JSON")
	}
	for _, key := range []string{"tools", "functions", "function_call", "tool_choice", "response_format", "audio", "modalities"} {
		v := string(req[key])
		if v != "" && v != "null" && v != "[]" && v != `"none"` {
			return nil, fmt.Errorf("Prism Chat Completions does not support %s", key)
		}
	}
	for _, key := range []string{"temperature", "top_p", "max_tokens", "max_completion_tokens", "stop", "seed", "presence_penalty", "frequency_penalty", "logprobs", "top_logprobs"} {
		if v := req[key]; len(v) > 0 && string(v) != "null" {
			return nil, fmt.Errorf("Prism cannot enforce %s", key)
		}
	}
	if n := string(req["n"]); n != "" && n != "1" {
		return nil, errors.New("Prism supports n=1 only")
	}
	var messages []map[string]json.RawMessage
	if json.Unmarshal(req["messages"], &messages) != nil || len(messages) == 0 {
		return nil, errors.New("messages must be a non-empty array")
	}
	for _, m := range messages {
		if string(m["role"]) == `"tool"` || len(m["tool_calls"]) > 0 || len(m["function_call"]) > 0 {
			return nil, errors.New("Prism generic Chat tool history is unverified")
		}
	}
	out := map[string]json.RawMessage{"model": req["model"], "input": req["messages"], "store": json.RawMessage("false")}
	if v := req["stream"]; len(v) > 0 {
		out["stream"] = v
	}
	if v := req["reasoning_effort"]; len(v) > 0 {
		out["reasoning"] = json.RawMessage(`{"effort":` + string(v) + `}`)
	}
	return json.Marshal(out)
}

func prismWriteChat(c *gin.Context, w *prismEventWriter, id, model, text string, usage *prism.Usage) error {
	var u any
	if usage != nil {
		u = map[string]any{"prompt_tokens": usage.InputTokens, "completion_tokens": usage.OutputTokens, "total_tokens": usage.TotalTokens}
	}
	if w == nil {
		c.JSON(200, map[string]any{"id": id, "object": "chat.completion", "created": time.Now().Unix(), "model": model, "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": text}, "finish_reason": "stop"}}, "usage": u})
		return nil
	}
	chunk := func(delta any, reason any, usage any) error {
		return w.data(map[string]any{"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": reason}}, "usage": usage})
	}
	if err := chunk(map[string]any{"role": "assistant", "content": text}, nil, nil); err != nil {
		return err
	}
	if err := chunk(map[string]any{}, "stop", u); err != nil {
		return err
	}
	_, err := c.Writer.WriteString("data: [DONE]\n\n")
	c.Writer.Flush()
	return err
}
