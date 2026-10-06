package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/prism"
)

// Native live progress is commentary, not the final answer. Preserve separate
// item identities, as facade/responses_progress.go does, and never stitch a
// changed cumulative snapshot onto previously emitted text.
type prismProgress struct {
	w         *prismEventWriter
	id, model string
	started   bool
	lines     []int
	texts     map[int]string
	items     []json.RawMessage
}

func (p *prismProgress) begin() error {
	if p.started {
		return nil
	}
	p.started = true
	if p.w.chat {
		return nil
	}
	r := prismResponseObject(p.id, p.model, []json.RawMessage{}, nil)
	r["status"] = "in_progress"
	if err := p.w.event("response.created", map[string]any{"response": r}); err != nil {
		return err
	}
	return p.w.event("response.in_progress", map[string]any{"response": r})
}

func (p *prismProgress) observe(st *prism.StatusResponse) error {
	if st.Reset && len(p.lines) > 0 {
		return errors.New("Prism reset after semantic output")
	}
	if p.texts == nil {
		p.texts = map[int]string{}
	}
	events := st.Progress
	if len(events) == 0 && st.Text != "" && (len(p.lines) == 0 || p.lines[0] == 2147483647) {
		events = []prism.LiveProgressEvent{{Type: "agent_message", LineIndex: 2147483647, Text: st.Text}}
	}
	for _, ev := range events {
		if ev.Type != "agent_message" || ev.LineIndex < 0 || ev.Text == "" {
			continue
		}
		old, exists := p.texts[ev.LineIndex]
		if exists && !strings.HasPrefix(ev.Text, old) {
			return errors.New("Prism rewrote emitted live progress")
		}
		// Hold the possible beginning of a fenced tool block until it is known
		// not to be the emulated protocol. Reasoning is never exposed here.
		if strings.Contains(ev.Text, "```codex-exec") {
			return errors.New("Prism leaked a tool block in text-only progress")
		}
		safe := ev.Text
		for n := len("```codex-exec") - 1; n > 0; n-- {
			if st.Done {
				break
			}
			if n > len(safe) {
				continue
			}
			if strings.HasSuffix(safe, "```codex-exec"[:n]) {
				safe = safe[:len(safe)-n]
				break
			}
		}
		if len(safe) <= len(old) {
			continue
		}
		if err := p.begin(); err != nil {
			return err
		}
		index := 0
		if !exists {
			index = len(p.lines)
			p.lines = append(p.lines, ev.LineIndex)
		} else {
			for i, k := range p.lines {
				if k == ev.LineIndex {
					index = i
					break
				}
			}
		}
		id := fmt.Sprintf("msg_%s_progress_%d", p.id, ev.LineIndex)
		if p.w.chat {
			delta := safe[len(old):]
			if !exists && index > 0 {
				delta = "\n" + delta
			}
			if err := p.chatDelta(delta); err != nil {
				return err
			}
		} else {
			if !exists {
				if err := p.w.event("response.output_item.added", map[string]any{"output_index": index, "item": map[string]any{"id": id, "type": "message", "role": "assistant", "phase": "commentary", "status": "in_progress", "content": []any{}}}); err != nil {
					return err
				}
				if err := p.w.event("response.content_part.added", map[string]any{"output_index": index, "item_id": id, "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}}); err != nil {
					return err
				}
			}
			if err := p.w.event("response.output_text.delta", map[string]any{"output_index": index, "item_id": id, "content_index": 0, "delta": safe[len(old):]}); err != nil {
				return err
			}
		}
		p.texts[ev.LineIndex] = safe
	}
	return nil
}

func (p *prismProgress) chatDelta(text string) error {
	return p.w.data(map[string]any{"id": p.id, "object": "chat.completion.chunk", "model": p.model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": text}, "finish_reason": nil}}})
}

func (p *prismProgress) finish() error {
	for i, line := range p.lines {
		id := fmt.Sprintf("msg_%s_progress_%d", p.id, line)
		text := p.texts[line]
		part := map[string]any{"type": "output_text", "text": text, "annotations": []any{}}
		item := map[string]any{"id": id, "type": "message", "role": "assistant", "phase": "commentary", "status": "completed", "content": []any{part}}
		if !p.w.chat {
			if err := p.w.event("response.output_text.done", map[string]any{"output_index": i, "item_id": id, "content_index": 0, "text": text}); err != nil {
				return err
			}
			if err := p.w.event("response.content_part.done", map[string]any{"output_index": i, "item_id": id, "content_index": 0, "part": part}); err != nil {
				return err
			}
			if err := p.w.event("response.output_item.done", map[string]any{"output_index": i, "item": item}); err != nil {
				return err
			}
		}
		b, _ := json.Marshal(item)
		p.items = append(p.items, b)
	}
	return nil
}
