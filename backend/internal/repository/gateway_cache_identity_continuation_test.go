package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestCodexIdentityContinuationSharedCache(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	writer, reader := &gatewayCache{rdb: client}, &gatewayCache{rdb: client}
	key := service.OpenAIIdentityContinuationKey(7, "ticket", "secret-ticket")
	require.NotContains(t, key, "secret-ticket")
	require.NotEqual(t, key, service.OpenAIIdentityContinuationKey(8, "ticket", "secret-ticket"))
	require.NotEqual(t, key, service.OpenAIIdentityContinuationKey(7, "response", "secret-ticket"))
	binding := service.OpenAIIdentityContinuation{CredentialNamespace: "credential-hash", APIKeyID: 77, UserID: 9, Revision: "revision", Mode: service.CodexIdentityPreserveClient}
	require.NoError(t, writer.SetOpenAIIdentityContinuation(context.Background(), key, binding, time.Minute))
	got, err := reader.GetOpenAIIdentityContinuation(context.Background(), key)
	require.NoError(t, err)
	require.Equal(t, &binding, got)
	mr.FastForward(time.Minute)
	got, err = reader.GetOpenAIIdentityContinuation(context.Background(), key)
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestCodexIdentityContinuationCannotOverwriteAnotherPolicy(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	writer, reader := &gatewayCache{rdb: client}, &gatewayCache{rdb: client}
	binding := service.OpenAIIdentityContinuation{CredentialNamespace: "credential-hash", APIKeyID: 77, UserID: 9, Revision: "revision", Mode: service.CodexIdentityPreserveClient}
	for _, field := range []string{"namespace", "key", "user", "revision", "mode"} {
		t.Run(field, func(t *testing.T) {
			key := service.OpenAIIdentityContinuationKey(7, "ticket", field)
			require.NoError(t, writer.SetOpenAIIdentityContinuation(context.Background(), key, binding, time.Minute))
			require.NoError(t, reader.SetOpenAIIdentityContinuation(context.Background(), key, binding, time.Minute))
			other := binding
			switch field {
			case "namespace":
				other.CredentialNamespace = "other"
			case "key":
				other.APIKeyID++
			case "user":
				other.UserID++
			case "revision":
				other.Revision = "other"
			case "mode":
				other.Mode = service.CodexIdentityIsolated
			}
			require.ErrorIs(t, writer.SetOpenAIIdentityContinuation(context.Background(), key, other, time.Minute), service.ErrCodexIdentityContinuationConflict)
			got, err := reader.GetOpenAIIdentityContinuation(context.Background(), key)
			require.NoError(t, err)
			require.Equal(t, &service.OpenAIIdentityContinuation{}, got, "ambiguous ticket must authorize neither policy")
			require.ErrorIs(t, writer.SetOpenAIIdentityContinuation(context.Background(), key, binding, time.Minute), service.ErrCodexIdentityContinuationConflict)
		})
	}
}
