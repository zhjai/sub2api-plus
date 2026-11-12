//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
)

func TestOpenAIExcelOAuthPartialAccountUpdateKeepsLoginClient(t *testing.T) {
	ctx := context.Background()
	repo := newSparkShadowRepoStub()
	a := excelBPSAccount(t)
	require.NoError(t, repo.Create(ctx, a))
	s := &adminServiceImpl{accountRepo: repo}
	incoming := map[string]any{"model_mapping": map[string]any{"bps-alias": "gpt-6-astra"}}
	updated, err := s.UpdateAccount(ctx, a.ID, &UpdateAccountInput{Credentials: incoming})
	require.NoError(t, err)
	require.True(t, updated.IsExcelOAuth())
	require.Equal(t, openai.ExcelClientID, updated.GetCredential("client_id"))
	require.NotEmpty(t, updated.GetOpenAIAccessToken())
	require.Len(t, incoming, 1, "validation must not merge old keys into the caller's map")
}

func TestOpenAIExcelOAuthAccountUpdateCannotChangeGrantProfile(t *testing.T) {
	for _, input := range []*UpdateAccountInput{
		{Credentials: map[string]any{"client_id": openai.ClientID}},
		{Credentials: map[string]any{"client_id": ""}},
		{Type: AccountTypeAPIKey},
		{Credentials: map[string]any{"client_id": openai.ExcelClientID, "access_token": "not-an-excel-token"}},
	} {
		ctx := context.Background()
		repo := newSparkShadowRepoStub()
		a := excelBPSAccount(t)
		require.NoError(t, repo.Create(ctx, a))
		s := &adminServiceImpl{accountRepo: repo}
		_, err := s.UpdateAccount(ctx, a.ID, input)
		require.Error(t, err)
		require.True(t, repo.accounts[a.ID].IsExcelOAuth())
	}
}
