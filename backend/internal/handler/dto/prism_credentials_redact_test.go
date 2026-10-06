package dto

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrismCredentialRedaction(t *testing.T) {
	input := map[string]any{"access_token": "synthetic-access", "refresh_token": "synthetic-refresh", "cookies": "synthetic-cookie", "session_token": "synthetic-session", "model_mapping": map[string]string{"alias": "model"}}
	output, status := RedactCredentials(input)
	data, err := json.Marshal(output)
	require.NoError(t, err)
	require.NotContains(t, string(data), "synthetic-")
	require.True(t, status["has_cookies"])
	require.True(t, status["has_session_token"])
	require.Equal(t, input["model_mapping"], output["model_mapping"])
	require.Equal(t, "synthetic-cookie", input["cookies"])
}
