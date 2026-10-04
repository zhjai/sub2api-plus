package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenAIEvalCandyOldVersionsCannotBecomeCurrentEvidence(t *testing.T) {
	enableQualityEffects(t)
	now := time.Now().UTC().Add(-time.Second)
	for _, version := range []string{
		"candy-29-v2",
		"sub2api-candy-21-v3-cpa-97623969-modeltrace-97623969",
	} {
		t.Run(version, func(t *testing.T) {
			quality := qualityTestAggregate(now, 17, 1, 1, 0)
			quality.DataVersion = version
			dimension, err := json.Marshal([]string{quality.Version, version, quality.RequestedModel, quality.ReasoningEffort, quality.TestType})
			require.NoError(t, err)
			digest := sha256.Sum256(dimension)
			oldKey := "openai_eval_quality_v2_" + hex.EncodeToString(digest[:])
			newKey := OpenAIEvalQualityExtraKeyFor(quality.RequestedModel, quality.ReasoningEffort, quality.TestType)
			require.NotEqual(t, oldKey, newKey)
			account := &Account{ID: 17, Extra: map[string]any{oldKey: quality}}
			_, ok := ReadOpenAIEvalQualityFromAccount(account, quality.RequestedModel, quality.ReasoningEffort, now)
			require.False(t, ok)
			// Even incorrectly copied old evidence must fail the version check.
			account.Extra[newKey] = quality
			_, ok = ReadOpenAIEvalQualityFromAccount(account, quality.RequestedModel, quality.ReasoningEffort, now)
			require.False(t, ok)
			delete(account.Extra, newKey)
			accounts := &qualityAccountRepository{account: account}
			svc := &OpenAIEvalService{accounts: accounts, repo: &qualityLeaseRepository{}}
			run := &OpenAIEvalRun{AccountID: 17, TestType: OpenAIEvalTypeCandy,
				RequestedModel: quality.RequestedModel, ReasoningEffort: quality.ReasoningEffort,
				DataVersion: version, TriggerSource: "scheduled", Status: "pass", FinishedAt: now}
			require.Error(t, svc.recordOpenAIEvalQuality(context.Background(), 2, run, quality.OpenAIEvalQualityCounts))
			require.Zero(t, accounts.writes)
			require.Equal(t, quality, account.Extra[oldKey])
			// A current result occupies a new namespace and preserves history.
			run.DataVersion = OpenAIEvalDataVersion
			require.NoError(t, svc.recordOpenAIEvalQuality(context.Background(), 3, run, quality.OpenAIEvalQualityCounts))
			got, ok := ReadOpenAIEvalQualityFromAccount(account, quality.RequestedModel, quality.ReasoningEffort, time.Now())
			require.True(t, ok)
			require.Equal(t, OpenAIEvalDataVersion, got.DataVersion)
			require.Equal(t, quality, account.Extra[oldKey])
			require.Len(t, account.Extra, 2)
		})
	}
}
