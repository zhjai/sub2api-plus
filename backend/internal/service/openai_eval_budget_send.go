package service

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type openAIEvalPhysicalSendKey struct{}
type openAIEvalPhysicalSend struct {
	id             string
	prepared       atomic.Bool
	traced         atomic.Bool
	connected      atomic.Bool
	wrote          atomic.Bool
	writeAttempted atomic.Bool
	once           sync.Once
}

// Pre-dial admission is a reservation. Response/trace evidence confirms a
// physical send; a proven failure before connecting refunds the reservation.
// Unknown transport writes retain conservative usage and invalidate quality.
// The caller must invoke finish exactly once after its single-send RoundTrip.
func openAIEvalTrackPhysicalSend(req *http.Request) (*http.Request, func(*http.Response, error) error) {
	c, _ := req.Context().Value(openAIEvalControllerKey{}).(*openAIEvalController)
	p := &openAIEvalPhysicalSend{id: generateRequestID()}
	ctx := context.WithValue(req.Context(), openAIEvalPhysicalSendKey{}, p)
	settle := func(action string) {
		p.once.Do(func() {
			if c == nil || !p.prepared.Load() {
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
		GetConn:      func(string) { p.traced.Store(true) },
		ConnectStart: func(string, string) { p.traced.Store(true) },
		WroteHeaders: func() { p.writeAttempted.Store(true) },
		GotConn:      func(httptrace.GotConnInfo) { p.connected.Store(true) },
		// Buffered headers alone do not prove a send. Charge immediately after
		// a successful flush, so in-flight upstream work is visible as sent.
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			p.writeAttempted.Store(true)
			if info.Err == nil {
				p.wrote.Store(true)
				settle("confirm")
			}
		},
	})
	return req.WithContext(ctx), func(resp *http.Response, err error) error {
		action := "uncertain"
		if resp != nil || p.wrote.Load() {
			action = "confirm"
		} else if err != nil && !p.connected.Load() && !p.writeAttempted.Load() && (p.traced.Load() || openAIEvalTypedConnectFailure(err)) {
			action = "refund"
		}
		settle(action)
		if action == "refund" {
			return openAIEvalConnectionNotSentError(err)
		}
		return err
	}
}

// Some transports preserve Go's typed dial error without delivering httptrace
// callbacks. Never infer non-send from arbitrary timeout text or a read/write
// failure: those can describe a request already accepted by the provider.
func openAIEvalTypedConnectFailure(err error) bool {
	var op *net.OpError
	if errors.As(err, &op) {
		return strings.EqualFold(op.Op, "dial")
	}
	var dns *net.DNSError
	return errors.As(err, &dns)
}
func openAIEvalConnectionNotSentError(err error) *OpenAIEvalRequestError {
	detail := "Connection establishment failed before an inference request was sent. Check the account proxy, DNS resolution and outbound network reachability."
	var dns *net.DNSError
	var op *net.OpError
	if errors.As(err, &dns) {
		detail = "DNS resolution failed before an inference request was sent. Check DNS and the selected account proxy's resolver."
	} else if errors.As(err, &op) && op.Timeout() {
		detail = "TCP connection timed out before an inference request was sent. Check the account proxy, DNS/fake-IP routing and outbound network reachability."
	}
	return &OpenAIEvalRequestError{Code: "evaluation_connection_not_sent", Message: detail, Attempted: false, Retryable: false}
}
