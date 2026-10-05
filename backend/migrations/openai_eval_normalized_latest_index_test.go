package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIEvalNormalizedLatestIndex(t *testing.T) {
	content, err := FS.ReadFile("244_openai_eval_normalized_latest_index_notx.sql")
	require.NoError(t, err)
	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_openai_eval_runs_normalized_latest")
	require.Contains(t, sql, "account_id, lower(btrim(requested_model)), lower(btrim(reasoning_effort)), test_type, finished_at DESC, id DESC")
	require.Contains(t, sql, "WHERE trigger_source IN ('manual', 'scheduled') AND finished_at IS NOT NULL AND status <> 'running'")
}
