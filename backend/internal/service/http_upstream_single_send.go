package service

import "context"

type httpUpstreamSingleSendKey struct{}

// HTTPUpstreamSingleSend is implemented only by transports that can suppress
// automatic connection retries, redirects, and provider fallback sends.
type HTTPUpstreamSingleSend interface {
	SupportsSingleSend() bool
}

// WithHTTPUpstreamSingleSend gives the evaluation loop sole ownership of retries.
// This policy is independent of account RPM and does not acquire an RPM permit.
func WithHTTPUpstreamSingleSend(ctx context.Context) context.Context {
	return context.WithValue(WithHTTPUpstreamRedirectsDisabled(ctx), httpUpstreamSingleSendKey{}, true)
}

func HTTPUpstreamSingleSendRequired(ctx context.Context) bool {
	return ctx != nil && ctx.Value(httpUpstreamSingleSendKey{}) == true
}

type HTTPUpstreamSingleSendUnsupportedError struct{}

func (*HTTPUpstreamSingleSendUnsupportedError) Error() string {
	return "single-send evaluation is unsupported by the selected upstream transport"
}
