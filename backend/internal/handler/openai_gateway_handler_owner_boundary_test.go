package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/testutil"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIResponsesWebSocketNonmovableOrdinaryOwnerCannotUseSessionAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fallback := openAIWSR5Upstream{frames: make(chan []byte, 1)}
	upstream := httptest.NewServer(http.HandlerFunc(fallback.handler))
	defer upstream.Close()
	accounts := []service.Account{
		openAIWSR5Account(9991, "ordinary-disabled-owner", "http://127.0.0.1:1", service.StatusDisabled, false, false, 1),
		openAIWSR5Account(9992, "ordinary-session-account", upstream.URL, service.StatusActive, true, false, 2),
	}
	cache := testutil.NewRedisGatewayCache(t)
	_, gateway, apiKey, server, _ := newOpenAIWSR5Handler(t, accounts, cache)
	defer server.Close()
	const responseID = "resp_unavailable_ordinary_owner"
	const payload = `{"type":"response.create","model":"gpt-5.1","previous_response_id":"resp_unavailable_ordinary_owner","input":[{"type":"function_call_output","call_id":"call_not_reconstructable","output":"done"}]}`
	ctx := context.Background()
	store := service.NewOpenAIWSStateStore(cache)
	require.NoError(t, store.BindResponseAccount(ctx, *apiKey.GroupID, responseID, accounts[0].ID, time.Hour))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, server.URL+"/openai/v1/responses", nil)
	sessionHash := gateway.GenerateSessionHash(c, []byte(payload))
	require.NoError(t, cache.SetSessionAccountID(ctx, *apiKey.GroupID, sessionHash, accounts[1].ID, time.Hour))
	conn := dialOpenAIWSR5(t, server)
	defer conn.CloseNow()
	writeOpenAIWSR5(t, conn, payload)
	err := readOpenAIWSR5(t, conn)
	var closed coderws.CloseError
	require.ErrorAs(t, err, &closed)
	require.Equal(t, coderws.StatusPolicyViolation, closed.Code)
	require.Contains(t, closed.Reason, "previous_response_id owner is unavailable")
	require.Zero(t, fallback.connections.Load(), "no dial or write may reach an unrelated account")
	owner, err := store.GetResponseAccount(ctx, *apiKey.GroupID, responseID)
	require.NoError(t, err)
	require.Equal(t, accounts[0].ID, owner)
}
