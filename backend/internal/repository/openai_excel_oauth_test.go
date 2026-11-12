package repository

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
)

func TestOpenAIExcelOAuthTokenRequests(t *testing.T) {
	for _, refresh := range []bool{false, true} {
		t.Run(map[bool]string{false: "exchange", true: "refresh"}[refresh], func(t *testing.T) {
			calls := 0
			server := newLocalTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				require.Equal(t, "true", r.URL.Query().Get("unified"))
				require.Equal(t, "preserved", r.URL.Query().Get("existing"))
				require.NoError(t, r.ParseForm())
				require.Equal(t, openai.ExcelClientID, r.PostForm.Get("client_id"))
				require.Empty(t, r.PostForm.Get("scope"))
				for _, h := range []string{"Originator", "Version", "X-Codex-Installation-Id", "Session-Id"} {
					require.Empty(t, r.Header.Get(h), h)
				}
				if !refresh {
					require.Equal(t, openai.ExcelRedirectURI, r.PostForm.Get("redirect_uri"))
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"access_token":"excel-at","refresh_token":"excel-rt","expires_in":3600}`)
			}))
			defer server.Close()
			client := &openaiOAuthService{tokenURL: server.URL + "/oauth/token?existing=preserved"}
			var err error
			if refresh {
				_, err = client.RefreshTokenWithClientID(context.Background(), "rt", "", openai.ExcelClientID)
			} else {
				_, err = client.ExchangeCode(context.Background(), "code", "verifier", openai.ExcelRedirectURI, "", openai.ExcelClientID)
			}
			require.NoError(t, err)
			require.Equal(t, 1, calls)
		})
	}
}
