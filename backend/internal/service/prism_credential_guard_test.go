package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrismCredentialGuardRejectsForgedIdentityAndAuth(t *testing.T) {
	ctx := context.Background()
	for _, key := range []string{"prism_verified_identity", "access_token", "refresh_token", "session_token", "cookies"} {
		require.Error(t, validatePrismCredentialWrite(ctx, map[string]any{key: "synthetic"}), key)
		require.NoError(t, validatePrismCredentialWrite(withVerifiedPrismCredentialWrite(ctx), map[string]any{key: "synthetic"}))
	}
	require.NoError(t, validatePrismCredentialWrite(ctx, nil))
	require.NoError(t, validatePrismCredentialWrite(ctx, map[string]any{"model_mapping": map[string]string{"alias": "actual"}}))
	existing := map[string]any{"prism_verified_identity": "verified", "oauth_client_id": "client"}
	require.NoError(t, validatePrismCredentialUpdate(ctx, existing, map[string]any{"prism_verified_identity": "verified", "oauth_client_id": "client", "model_mapping": map[string]string{"alias": "actual"}}))
	require.Error(t, validatePrismCredentialUpdate(ctx, existing, map[string]any{"prism_verified_identity": "forged"}))
}
