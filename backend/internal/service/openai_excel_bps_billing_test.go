package service

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
	"testing"
)

type bpsAttemptDedupBillingRepo struct {
	UsageBillingRepository
	seen    map[string]bool
	applied int
	charged float64
}

func (r *bpsAttemptDedupBillingRepo) Apply(_ context.Context, cmd *UsageBillingCommand) (*UsageBillingApplyResult, error) {
	if r.seen == nil {
		r.seen = map[string]bool{}
	}
	if r.seen[cmd.RequestID] {
		return &UsageBillingApplyResult{Applied: false}, nil
	}
	r.seen[cmd.RequestID] = true
	r.applied++
	r.charged += cmd.BalanceCost
	return &UsageBillingApplyResult{Applied: true}, nil
}
func TestExcelBPSRecordUsageDistinctAttemptsAndResubmissionDedup(t *testing.T) {
	logs := &openAIRecordUsageLogRepoStub{}
	repo := &bpsAttemptDedupBillingRepo{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, repo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
	ctx := context.WithValue(context.WithValue(context.Background(), ctxkey.ClientRequestID, "shared-client"), ctxkey.RequestID, "shared-local")
	account := excelBPSAccount(t)
	input := &OpenAIRecordUsageInput{Result: &OpenAIForwardResult{RequestID: "upstream-shared", Model: "gpt-5.1", Usage: OpenAIUsage{InputTokens: 19, OutputTokens: 5}}, APIKey: &APIKey{ID: 100}, User: &User{ID: 200}, Account: account, BPSAttemptBillingID: "bps:attempt1"}
	require.NoError(t, svc.RecordUsage(ctx, input))
	require.Equal(t, "bps:attempt1", logs.lastLog.RequestID)
	firstCharge := repo.charged
	require.Positive(t, firstCharge)
	second := *input
	second.BPSAttemptBillingID = "bps:attempt2"
	// Same-account retry also consumes independently; no account dimension can
	// substitute for the actual attempt dimension in the durable billing key.
	require.NoError(t, svc.RecordUsage(ctx, &second))
	require.Equal(t, 2, repo.applied)
	require.InDelta(t, 2*firstCharge, repo.charged, 1e-12)
	require.NoError(t, svc.RecordUsage(ctx, input))
	require.Equal(t, 2, repo.applied, "same attempt resubmission must remain idempotent")
	require.InDelta(t, 2*firstCharge, repo.charged, 1e-12)
	require.Equal(t, "upstream-shared", input.Result.RequestID)
	native := *input
	native.Account = privacyTestAccount(AccountTypeAPIKey)
	require.Equal(t, "client:shared-client", resolveOpenAIUsageBillingRequestID(ctx, &native), "native billing identity unchanged")
}
