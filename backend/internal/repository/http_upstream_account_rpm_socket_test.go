package repository

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

func hardRPMSocketRequest(t *testing.T, url, bodyKind string, limited bool, used *atomic.Int64) *http.Request {
	t.Helper()
	var body io.Reader
	method := http.MethodGet
	if bodyKind == "body" {
		body = strings.NewReader(`{"input":"synthetic"}`)
		method = http.MethodPost
	} else if bodyKind == "no_body" {
		body = http.NoBody
	}
	ctx := context.Background()
	if limited {
		ctx = service.WithAccountRPMHTTPAdmission(ctx, func(context.Context) error {
			used.Add(1)
			return nil
		})
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	require.NoError(t, err)
	// Even idempotent requests must never be replayed without admission.
	req.Header.Set("Idempotency-Key", "synthetic-local-test")
	return req
}

func TestAccountHardRPMHTTP1SocketNoHiddenResend(t *testing.T) {
	for _, bodyKind := range []string{"nil", "no_body", "body"} {
		for _, limited := range []bool{false, true} {
			t.Run(bodyKind+map[bool]string{false: "/unlimited", true: "/limited"}[limited], func(t *testing.T) {
				var sends, used atomic.Int64
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.Copy(io.Discard, r.Body)
					if r.URL.Path != "/warm" && sends.Add(1) == 1 {
						conn, _, err := w.(http.Hijacker).Hijack()
						if err == nil {
							_ = conn.Close()
						}
						return
					}
					_, _ = io.WriteString(w, "ok")
				}))
				defer server.Close()
				base := &http.Transport{}
				defer base.CloseIdleConnections()
				client := httpClientWithAccountRPMAdmission(&http.Client{Transport: base, Timeout: 5 * time.Second})
				warm, err := client.Get(server.URL + "/warm")
				require.NoError(t, err)
				_, err = io.Copy(io.Discard, warm.Body)
				require.NoError(t, err)
				require.NoError(t, warm.Body.Close())
				resp, err := client.Do(hardRPMSocketRequest(t, server.URL+"/retry", bodyKind, limited, &used))
				if limited {
					require.Error(t, err)
					require.EqualValues(t, 1, sends.Load())
					require.EqualValues(t, 1, used.Load())
				} else {
					require.NoError(t, err)
					require.EqualValues(t, 2, sends.Load(), "control must exercise Go's automatic replay")
					require.Zero(t, used.Load())
				}
				if resp != nil {
					_ = resp.Body.Close()
				}
			})
		}
	}
}

// A local HTTP/2 peer deliberately refuses the first transmitted stream. Counting
// HEADERS at the peer proves wire attempts, including requests with no body.
func hardRPMHTTP2Peer(t *testing.T, mode string, sends *atomic.Int64) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				reader := bufio.NewReader(conn)
				preface := make([]byte, len(http2.ClientPreface))
				if _, err := io.ReadFull(reader, preface); err != nil || string(preface) != http2.ClientPreface {
					return
				}
				framer := http2.NewFramer(conn, reader)
				if framer.WriteSettings() != nil {
					return
				}
				var refusedStream uint32
				for {
					frame, err := framer.ReadFrame()
					if err != nil {
						return
					}
					switch f := frame.(type) {
					case *http2.SettingsFrame:
						if !f.IsAck() {
							_ = framer.WriteSettingsAck()
						}
						continue
					case *http2.HeadersFrame:
						if sends.Add(1) == 1 {
							refusedStream = f.StreamID
							if !f.StreamEnded() {
								continue
							}
						} else {
							var headers bytes.Buffer
							encoder := hpack.NewEncoder(&headers)
							_ = encoder.WriteField(hpack.HeaderField{Name: ":status", Value: "200"})
							_ = framer.WriteHeaders(http2.HeadersFrameParam{StreamID: f.StreamID, BlockFragment: headers.Bytes(), EndHeaders: true, EndStream: true})
							continue
						}
					case *http2.DataFrame:
						if f.StreamID != refusedStream || !f.StreamEnded() {
							continue
						}
					default:
						continue
					}
					if refusedStream != 0 {
						if mode == "goaway" {
							_ = framer.WriteGoAway(0, http2.ErrCodeNo, nil)
							return
						}
						_ = framer.WriteRSTStream(refusedStream, http2.ErrCodeRefusedStream)
						refusedStream = 0
					}
				}
			}()
		}
	}()
	t.Cleanup(func() { _ = ln.Close(); wg.Wait() })
	return "http://" + ln.Addr().String()
}

func TestAccountHardRPMHTTP2SocketNoHiddenResend(t *testing.T) {
	for _, mode := range []string{"refused_stream", "goaway"} {
		for _, bodyKind := range []string{"nil", "no_body", "body"} {
			for _, limited := range []bool{false, true} {
				t.Run(mode+"/"+bodyKind+map[bool]string{false: "/unlimited", true: "/limited"}[limited], func(t *testing.T) {
					var sends, used atomic.Int64
					url := hardRPMHTTP2Peer(t, mode, &sends)
					protocols := &http.Protocols{}
					protocols.SetUnencryptedHTTP2(true)
					base := &http.Transport{Protocols: protocols}
					defer base.CloseIdleConnections()
					client := httpClientWithAccountRPMAdmission(&http.Client{Transport: base, Timeout: 5 * time.Second})
					resp, err := client.Do(hardRPMSocketRequest(t, url, bodyKind, limited, &used))
					if limited {
						require.Error(t, err)
						require.EqualValues(t, 1, sends.Load())
						require.EqualValues(t, 1, used.Load())
					} else {
						require.NoError(t, err)
						require.EqualValues(t, 2, sends.Load(), "control must exercise Go's automatic replay")
						require.Equal(t, 2, resp.ProtoMajor)
						require.Zero(t, used.Load())
					}
					if resp != nil {
						_ = resp.Body.Close()
					}
				})
			}
		}
	}
}

func TestAccountHardRPMProductionTLSProfilesAndPostDialAdmission(t *testing.T) {
	var sends atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		sends.Add(1)
		_, _ = io.WriteString(w, "ok")
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	for _, mode := range []string{upstreamProtocolModeDefault, upstreamProtocolModeOpenAIH1, upstreamProtocolModeOpenAIH2, upstreamProtocolModeLongStreamH2} {
		t.Run(mode, func(t *testing.T) {
			base, err := buildUpstreamTransport(http2KeepAliveTestPoolSettings(), nil, mode)
			require.NoError(t, err)
			defer base.CloseIdleConnections()
			if mode == upstreamProtocolModeOpenAIH2 || mode == upstreamProtocolModeLongStreamH2 {
				cloned := base.Clone()
				require.NotNil(t, cloned.HTTP2)
				require.Positive(t, cloned.HTTP2.SendPingTimeout)
				require.Positive(t, cloned.HTTP2.PingTimeout)
			}
			roots := x509.NewCertPool()
			roots.AddCert(server.Certificate())
			base.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
			var dialed atomic.Bool
			dial := base.DialContext
			base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				conn, err := dial(ctx, network, address)
				if err == nil {
					dialed.Store(true)
				}
				return conn, err
			}
			client := httpClientWithAccountRPMAdmission(&http.Client{Transport: base, Timeout: 5 * time.Second})
			for _, deny := range []bool{false, true} {
				dialed.Store(false)
				ctx := service.WithAccountRPMHTTPAdmission(context.Background(), func(context.Context) error {
					require.True(t, dialed.Load(), "fresh admission must follow dialing")
					if deny {
						return &service.AccountRPMError{AccountID: 1}
					}
					return nil
				})
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
				require.NoError(t, err)
				before := sends.Load()
				resp, err := client.Do(req)
				require.Equal(t, 10, base.MaxConnsPerHost, "limited dispatch must not mutate the shared unlimited transport")
				if deny {
					require.True(t, service.IsAccountRPMError(err))
					require.Equal(t, before, sends.Load())
				} else {
					require.NoError(t, err)
					wantProto := 1
					if mode == upstreamProtocolModeOpenAIH2 || mode == upstreamProtocolModeLongStreamH2 {
						wantProto = 2
					}
					require.Equal(t, wantProto, resp.ProtoMajor)
					require.NoError(t, resp.Body.Close())
					require.Equal(t, before+1, sends.Load())
				}
			}
		})
	}
}
