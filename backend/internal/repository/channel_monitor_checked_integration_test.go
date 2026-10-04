//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestChannelMonitorCheckPersistenceDoesNotInvalidateFreshHistory(t *testing.T) {
	ctx := context.Background()
	repo := NewChannelMonitorRepository(integrationEntClient, integrationDB)
	m := &service.ChannelMonitor{
		Name: "ranking-freshness-regression", Provider: service.MonitorProviderOpenAI,
		Endpoint: "https://synthetic.invalid/v1", APIKey: "synthetic-ciphertext", PrimaryModel: "upstream-model",
		Enabled: true, IntervalSeconds: 300, CheckMode: service.MonitorCheckModeProbe,
	}
	require.NoError(t, repo.Create(ctx, m))
	t.Cleanup(func() { _ = repo.Delete(ctx, m.ID) })
	configuredAt := m.UpdatedAt
	checkedAt := time.Now().UTC()
	require.NoError(t, repo.InsertHistoryBatch(ctx, []*service.ChannelMonitorHistoryRow{
		{MonitorID: m.ID, Model: m.PrimaryModel, Status: service.MonitorStatusOperational, CheckedAt: checkedAt},
	}))
	require.NoError(t, repo.MarkChecked(ctx, m.ID, checkedAt))
	loaded, err := repo.GetByID(ctx, m.ID)
	require.NoError(t, err)
	require.WithinDuration(t, configuredAt, loaded.UpdatedAt, time.Microsecond)
	require.WithinDuration(t, checkedAt, *loaded.LastCheckedAt, time.Microsecond)
	history, err := repo.ListHistory(ctx, m.ID, m.PrimaryModel, 1)
	require.NoError(t, err)
	require.Len(t, history, 1)
	require.False(t, history[0].CheckedAt.Before(loaded.UpdatedAt), "normal persistence must leave current probe evidence valid")
	loaded.PrimaryModel = "changed-upstream-model"
	require.NoError(t, repo.Update(ctx, loaded))
	require.True(t, loaded.UpdatedAt.After(history[0].CheckedAt), "real configuration edits must still invalidate old history")
}
