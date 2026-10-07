package service

import (
	"context"
	"net/http"
	"net/http/httptrace"
	"sync"
	"sync/atomic"
	"time"
)

type openAIEvalPhysicalSendKey struct{}
type openAIEvalPhysicalSend struct {
	id        string
	prepared  atomic.Bool
	traced    atomic.Bool
	connected atomic.Bool
	wrote     atomic.Bool
	once      sync.Once
}

// Pre-dial admission is a reservation. Response/trace evidence confirms a
// physical send; a proven failure before connecting refunds the reservation.
// Unknown transport writes retain conservative usage and invalidate quality.
// The caller must invoke finish exactly once after its single-send RoundTrip.
func openAIEvalTrackPhysicalSend(req *http.Request) (*http.Request, func(*http.Response, error)) {
	c, _ := req.Context().Value(openAIEvalControllerKey{}).(*openAIEvalController)
	if c == nil {
		return req, func(*http.Response, error) {}
	}
	p := &openAIEvalPhysicalSend{id: generateRequestID()}
	ctx := context.WithValue(req.Context(), openAIEvalPhysicalSendKey{}, p)
	settle := func(action string) {
		p.once.Do(func() {
			if !p.prepared.Load() {
				return
			}
			if action == "confirm" {
				c.mu.Lock()
				c.diagnostics.Sent++
				c.mu.Unlock()
			}
			if c.store != nil {
				cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				defer cancel()
				decision, storeErr := c.store.OpenAIEvalBudget(cleanup, c.namespace, OpenAIEvalBudgetOperation{Action: action, Owner: c.owner, SendID: p.id})
				if storeErr != nil || !decision.Allowed {
					_ = c.deny("evaluation_send_accounting_unavailable")
					return
				}
			}
			if action == "uncertain" {
				_ = c.deny("evaluation_send_unknown")
			}
		})
	}
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GetConn: func(string) { p.traced.Store(true) },
		GotConn: func(httptrace.GotConnInfo) { p.connected.Store(true) },
		// Buffered headers alone do not prove a send. Charge immediately after
		// a successful flush, so in-flight upstream work is visible as sent.
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err == nil {
				p.wrote.Store(true)
				settle("confirm")
			}
		},
	})
	return req.WithContext(ctx), func(resp *http.Response, err error) {
		action := "uncertain"
		if resp != nil || p.wrote.Load() {
			action = "confirm"
		} else if p.traced.Load() && !p.connected.Load() {
			action = "refund"
		}
		settle(action)
	}
}
