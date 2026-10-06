package repository

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestEvaluationSingleSendAdmitsExactlyOnce(t *testing.T) {
	var sends, permits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sends.Add(1)
		_, _ = io.WriteString(w, "OK")
	}))
	defer server.Close()
	client := httpClientWithAccountRPMAdmission(&http.Client{Transport: &http.Transport{}})
	defer client.CloseIdleConnections()
	ctx := service.WithHTTPUpstreamSingleSend(t.Context())
	ctx = service.WithAccountRPMHTTPAdmission(ctx, func(context.Context) error {
		if permits.Add(1) > 1 {
			return &service.AccountRPMError{AccountID: 91}
		}
		return nil
	})
	for i := 0; i < 2; i++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, strings.NewReader("{}"))
		require.NoError(t, err)
		resp, err := client.Do(req)
		if i == 0 {
			require.NoError(t, err)
			_, _ = io.Copy(io.Discard, resp.Body)
			require.NoError(t, resp.Body.Close())
		} else {
			require.Error(t, err)
			require.True(t, service.IsAccountRPMError(err))
		}
	}
	require.EqualValues(t, 2, permits.Load())
	require.EqualValues(t, 1, sends.Load())
}
