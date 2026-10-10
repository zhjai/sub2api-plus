package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestRetiredPrismGatewayNeverFallsThrough(t *testing.T) {
	router := newGatewayRoutesTestRouter(service.PlatformPrism)
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/v1/responses"},
		{http.MethodPost, "/responses"},
		{http.MethodPost, "/backend-api/codex/responses"},
		{http.MethodPost, "/v1/chat/completions"},
		{http.MethodPost, "/chat/completions"},
		{http.MethodPost, "/v1/messages"},
		{http.MethodPost, "/v1/responses/compact"},
		{http.MethodGet, "/v1/responses"},
		{http.MethodGet, "/backend-api/codex/models"},
		{http.MethodGet, "/models"},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"model":"gpt-6-astra","input":"hi"}`))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(rec, req)
			require.Equal(t, http.StatusGone, rec.Code)
			require.Contains(t, rec.Body.String(), "channel_removed")
		})
	}
}
