package handler

import (
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIResponses_BPSContinuationPreservesTenantOwnerError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, sameTenant := range []bool{true, false} {
		name := "creating tenant gets full history guidance"
		if !sameTenant {
			name = "another tenant gets ownership error"
		}
		t.Run(name, func(t *testing.T) {
			account := service.Account{ID: 9998, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
				Status: service.StatusActive, Schedulable: true, Concurrency: 1,
				Credentials: map[string]any{"client_id": openai.ExcelClientID}}
			cache := testutil.NewRedisGatewayCache(t)
			upstream := &openAIHTTPR5WrongModelUpstream{}
			_, gateway, apiKey, server, _ := newOpenAIR5Handler(t, []service.Account{account}, cache, upstream)
			defer server.Close()
			const responseID = "resp_bps_owned_continuation"
			ownerUser, ownerKey := apiKey.User.ID, apiKey.ID
			if !sameTenant {
				ownerUser++
				ownerKey++
			}
			require.NoError(t, gateway.BindOpenAIHTTPResponseOwner(t.Context(), *apiKey.GroupID, responseID, ownerUser, ownerKey))
			store := service.NewOpenAIWSStateStore(cache)
			require.NoError(t, store.BindResponseAccount(t.Context(), *apiKey.GroupID, responseID, account.ID, time.Hour))
			status, body := r9PostResponses(t, server, "bps-owner-test", `{"model":"gpt-6-astra","stream":false,"previous_response_id":"resp_bps_owned_continuation","input":"continue"}`)
			require.Equal(t, http.StatusBadRequest, status)
			if sameTenant {
				require.Contains(t, string(body), "BPS requires expanded conversation history; previous_response_id is not supported")
			} else {
				require.Contains(t, string(body), "previous_response_id is not available for this user")
				require.NotContains(t, string(body), "BPS")
			}
			require.Zero(t, upstream.calls.Load())
		})
	}
}
