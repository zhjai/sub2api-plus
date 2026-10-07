package repository

import (
	"errors"
	"io"
	"net"
	"net/http"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (*httpUpstreamService) HandlesAccountRPMAdmission() bool { return true }
func (*httpUpstreamService) SupportsSingleSend() bool         { return true }

type accountRPMTransport struct{ base http.RoundTripper }

func (t *accountRPMTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if service.HTTPUpstreamSingleSendRequired(req.Context()) {
		if transport, ok := t.base.(*http.Transport); ok {
			return accountRPMSingleSend(transport, req)
		}
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, &service.HTTPUpstreamSingleSendUnsupportedError{}
	}
	limited, err := service.AccountRPMHTTPIsLimited(req.Context())
	if err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	if limited {
		if transport, ok := t.base.(*http.Transport); ok {
			return accountRPMSingleSend(transport, req)
		}
	} else {
		observe := service.ObserveCodexOutboundAttempt(req, "native_http")
		resp, err := t.base.RoundTrip(req)
		observe(resp, err)
		return resp, err
	}
	if req.Body != nil {
		_ = req.Body.Close()
	}
	return nil, &service.AccountRPMError{Unavailable: true, Cause: errors.New("unsupported account RPM HTTP transport")}
}

// Go's Transport.RoundTrip can replay within one invocation (HTTP/1 reused
// connections and HTTP/2 REFUSED_STREAM/GOAWAY). Go 1.27 ClientConn.RoundTrip
// directly invokes one connection's RoundTrip and has no such retry loop.
// The original transport supplies proxy, TLS fingerprint and protocol settings;
// unlimited requests retain its normal pooling and automatic retries.
func accountRPMSingleSend(transport *http.Transport, req *http.Request) (*http.Response, error) {
	// Go 1.27 does not increment the pool's connection count for ClientConn,
	// but its HTTP/1 close path still decrements it when this limit is nonzero.
	// Use an isolated clone; the shared unlimited transport must stay untouched.
	transport = transport.Clone()
	transport.MaxConnsPerHost = 0
	evaluation := service.HTTPUpstreamSingleSendRequired(req.Context())
	// Evaluation performs nonblocking admission before dialing so its outer
	// wait can release capacity without retaining a connection. Business
	// requests retain their post-dial admission contract.
	if evaluation {
		if err := service.AdmitAccountRPMHTTPRetry(req.Context()); err != nil {
			if req.Body != nil {
				_ = req.Body.Close()
			}
			return nil, err
		}
	}
	port := req.URL.Port()
	if port == "" {
		port = "443"
		if req.URL.Scheme == "http" {
			port = "80"
		}
	}
	conn, err := transport.NewClientConn(req.Context(), req.URL.Scheme, net.JoinHostPort(req.URL.Hostname(), port))
	if err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	if !evaluation {
		if err := service.AdmitAccountRPMHTTPRetry(req.Context()); err != nil {
			_ = conn.Close()
			if req.Body != nil {
				_ = req.Body.Close()
			}
			return nil, err
		}
	}
	observe := service.ObserveCodexOutboundAttempt(req, "native_http")
	resp, err := conn.RoundTrip(req)
	observe(resp, err)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	resp.Body = &accountRPMConnBody{ReadCloser: resp.Body, conn: conn}
	return resp, nil
}

type accountRPMConnBody struct {
	io.ReadCloser
	conn *http.ClientConn
	once sync.Once
}

func (b *accountRPMConnBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(func() { _ = b.conn.Close() })
	return err
}

func (b *accountRPMConnBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.once.Do(func() { _ = b.conn.Close() })
	}
	return n, err
}

func httpClientWithAccountRPMAdmission(client *http.Client) *http.Client {
	if client == nil {
		return nil
	}
	clone := *client
	base := clone.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	clone.Transport = &accountRPMTransport{base: base}
	return &clone
}
