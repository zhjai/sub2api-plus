package repository

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type hardRPMRoundTripFunc func(*http.Request) (*http.Response, error)

func (f hardRPMRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAccountHardRPMTransportRedirectAndHiddenRetry(t *testing.T) {
	for _, mode := range []string{"redirect", "grok"} {
		for _, limit := range []int{1, 2} {
			t.Run(mode+string(rune('0'+limit)), func(t *testing.T) {
				used := 0
				var sends atomic.Int64
				ctx := service.WithAccountRPMHTTPAdmission(context.Background(), func(context.Context) error {
					if used >= limit {
						return &service.AccountRPMError{AccountID: 1}
					}
					used++
					return nil
				})
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					_, _ = io.Copy(io.Discard, req.Body)
					n := sends.Add(1)
					status := 200
					body := "{}"
					if n == 1 {
						if mode == "redirect" {
							status = 307
							w.Header().Set("Location", "https://example.invalid/second")
						} else {
							status = 403
							body = `{"error":"access denied"}`
						}
					}
					w.WriteHeader(status)
					_, _ = io.WriteString(w, body)
				}))
				defer server.Close()
				base := server.Client().Transport.(*http.Transport).Clone()
				base.TLSClientConfig.ServerName = "example.com"
				base.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
				}
				defer base.CloseIdleConnections()
				client := httpClientWithAccountRPMAdmission(&http.Client{Transport: base})
				client = httpClientWithGrokAccessDeniedFallback(client)
				u := "https://example.invalid/first"
				if mode == "grok" {
					u = "https://cli-chat-proxy.grok.com/v1/responses"
				}
				req, err := http.NewRequestWithContext(ctx, "POST", u, strings.NewReader(`{"model":"test"}`))
				require.NoError(t, err)
				req.Header.Set("Authorization", "Bearer synthetic")
				req.Header.Set("X-XAI-Token-Auth", "xai-grok-cli")
				resp, err := client.Do(req)
				if limit == 1 {
					require.True(t, service.IsAccountRPMError(err))
				} else {
					require.NoError(t, err)
					require.Equal(t, 200, resp.StatusCode)
				}
				if resp != nil && resp.Body != nil {
					resp.Body.Close()
				}
				require.EqualValues(t, limit, sends.Load())
				require.EqualValues(t, sends.Load(), used)
			})
		}
	}
}

func TestAccountHardRPMUnknownTransportFailsClosed(t *testing.T) {
	sends := 0
	client := httpClientWithAccountRPMAdmission(&http.Client{Transport: hardRPMRoundTripFunc(func(*http.Request) (*http.Response, error) {
		sends++
		return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
	})})
	ctx := service.WithAccountRPMHTTPAdmission(context.Background(), func(context.Context) error { return nil })
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.invalid", nil)
	require.NoError(t, err)
	_, err = client.Do(req)
	require.True(t, service.IsAccountRPMError(err))
	require.Zero(t, sends)
	resp, err := client.Do(req.WithContext(context.Background()))
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, 1, sends)
}

func TestAccountHardRPMRepositoryRejectsInvalidWritesBeforeIO(t *testing.T) {
	r := &accountRepository{}
	ctx := context.Background()
	bad := map[string]any{"rpm_limit": "20"}
	require.Error(t, r.Create(ctx, &service.Account{Extra: bad}))
	require.Error(t, r.CreateWithAccountGroups(ctx, &service.Account{Extra: bad}, nil))
	require.Error(t, r.UpdateExtra(ctx, 1, bad))
	_, err := r.BulkUpdate(ctx, []int64{1}, service.AccountBulkUpdate{Extra: bad})
	require.Error(t, err)
}
