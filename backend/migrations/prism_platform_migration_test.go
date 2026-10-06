package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrismPlatformMigrationPreservesAllPlatforms(t *testing.T) {
	data, err := FS.ReadFile("245_add_prism_platform.sql")
	require.NoError(t, err)
	sql := strings.Join(strings.Fields(string(data)), " ")
	for _, platform := range []string{"anthropic", "openai", "gemini", "antigravity", "grok", "kimi", "zhipu", "deepseek", "minimax", "opencode_go", "typesafe", "prism"} {
		require.Equal(t, 2, strings.Count(sql, "'"+platform+"'"), platform)
	}
	require.Contains(t, sql, "DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check")
	require.Contains(t, sql, "DROP CONSTRAINT IF EXISTS composite_model_routes_target_platform_check")
}
