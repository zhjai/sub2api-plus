package service

import "context"

type prismLifecycleContextKey struct{}

func withPrismLifecycle(ctx context.Context, lifecycle *PrismAccountService) context.Context {
	if lifecycle == nil {
		return ctx
	}
	return context.WithValue(ctx, prismLifecycleContextKey{}, lifecycle)
}

// Attach the same lifecycle instance used by the admin handler; this preserves
// the long-standing constructor signatures and unifies refresh races.
func (s *OpenAIGatewayService) SetPrismAccountService(lifecycle *PrismAccountService) {
	s.prismAccountService = lifecycle
}

func (s *GatewayService) SetPrismGateway(gateway *OpenAIGatewayService) {
	s.prismGateway = gateway
}
