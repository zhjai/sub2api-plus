package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// Both admission gates must survive an upstream merge: an owning account can
// still lack exec, while a healthy non-owner must not serve a composite alias.
func TestOpenAISchedulerCompositeOwnershipPreservesExecCooldown(t *testing.T) {
	owner := &Account{
		ID: 89001, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"model_mapping": map[string]any{
			"team-astra": "gpt-6-astra", "team-sol": "gpt-6-sol",
		}},
	}
	nonOwner := &Account{
		ID: 89002, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true,
	}
	svc := &OpenAIGatewayService{}
	scheduler := &defaultOpenAIAccountScheduler{service: svc}
	svc.ObserveOpenAIExecProtocolLeak(owner, "gpt-6-astra", "session-a")
	svc.ObserveOpenAIExecProtocolLeak(owner, "gpt-6-astra", "session-b")

	for _, tc := range []struct {
		name    string
		account *Account
		model   string
		exec    bool
		reason  string
	}{
		{"non-owner cannot serve plain requests", nonOwner, "team-astra", false, "account_model_not_owned"},
		{"non-owner cannot serve exec requests", nonOwner, "team-astra", true, "account_model_not_owned"},
		{"owner remains blocked for exec", owner, "team-astra", true, "exec_capability_cooldown"},
		{"owner remains eligible for plain requests", owner, "team-astra", false, ""},
		{"exec cooldown is model scoped", owner, "team-sol", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := WithCompositeRouteDecision(context.Background(), CompositeRouteDecision{
				Matched: true, Source: CompositeRouteSourceAccount,
				PublicModel: tc.model, TargetPlatform: PlatformOpenAI, UpstreamModel: tc.model,
			})
			ctx = withOpenAIExecCapability(ctx, tc.exec)
			allowed, reason := scheduler.isAccountRequestCompatibleReason(ctx, tc.account, OpenAIAccountScheduleRequest{RequestedModel: tc.model})
			require.Equal(t, tc.reason == "", allowed)
			require.Equal(t, tc.reason, reason)
		})
	}

	svc.ObserveOpenAIExecCapabilityResult(owner, "gpt-6-astra", &OpenAIForwardResult{ExecCallObserved: true})
	ctx := WithCompositeRouteDecision(context.Background(), CompositeRouteDecision{
		Matched: true, Source: CompositeRouteSourceAccount,
		PublicModel: "team-astra", TargetPlatform: PlatformOpenAI, UpstreamModel: "team-astra",
	})
	allowed, reason := scheduler.isAccountRequestCompatibleReason(withOpenAIExecCapability(ctx, true), owner, OpenAIAccountScheduleRequest{RequestedModel: "team-astra"})
	require.True(t, allowed, "an observed exec call recovers only the capability gate")
	require.Empty(t, reason)
}
