package repository

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

// Install only a local dial destination and local certificate roots on a client
// built by the production factory. Do/DoWithTLS and all wrappers stay intact.
func r13EvalProductionUpstream(t *testing.T, target string, cert *x509.Certificate, h2 bool) (*httpUpstreamService, *service.AccountTestService, *service.OpenAIEvalTarget) {
	t.Helper()
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIHTTP2.Enabled = h2
	up := NewHTTPUpstream(cfg).(*httpUpstreamService)
	entry, err := up.getClientEntry("", 91, 1, service.HTTPUpstreamProfileOpenAI, false, false)
	require.NoError(t, err)
	tr := entry.client.Transport.(*http.Transport)
	t.Cleanup(tr.CloseIdleConnections)
	u, err := url.Parse(target)
	require.NoError(t, err)
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, u.Host)
	}
	if cert != nil {
		roots := x509.NewCertPool()
		roots.AddCert(cert)
		tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: "example.com"}
	}
	a := &service.Account{ID: 91, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{"api_key": "synthetic", "base_url": target}, Extra: map[string]any{"rpm_limit": 1}}
	svc := service.NewAccountTestService(nil, nil, nil, nil, nil, up, cfg, &service.TLSFingerprintProfileService{})
	svc.SetOpenAIGatewayService(&service.OpenAIGatewayService{})
	return up, svc, &service.OpenAIEvalTarget{Account: a, Credential: a, RequestedModel: "gpt-6-astra", UpstreamModel: "gpt-6-astra"}
}

func TestR13EvalHTTP1ActualAttemptsAndRedirect(t *testing.T) {
	for _, mode := range []string{"drop", "redirect", "success"} {
		t.Run(mode, func(t *testing.T) {
			var sends, redirected atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if r.URL.Path == "/warm" {
					_, _ = io.WriteString(w, "warm")
					return
				}
				if r.URL.Path == "/redirected" {
					redirected.Add(1)
				}
				sends.Add(1)
				switch mode {
				case "drop":
					c, _, err := w.(http.Hijacker).Hijack()
					if err == nil {
						_ = c.Close()
					}
				case "redirect":
					w.Header().Set("Location", "/redirected")
					w.WriteHeader(http.StatusTemporaryRedirect)
				default:
					_, _ = io.WriteString(w, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"OK"}]}]}`)
				}
			}))
			defer server.Close()
			up, svc, target := r13EvalProductionUpstream(t, server.URL, nil, false)
			// Prove eval is single-send even when account RPM is unlimited. The
			// idempotency header makes this POST replayable on a reused H1 conn.
			target.Account.Extra["rpm_limit"] = 0
			target.Account.Credentials["header_override_enabled"] = true
			target.Account.Credentials["header_overrides"] = map[string]any{"Idempotency-Key": "synthetic-r13"}
			// Seed a reusable production connection, the HTTP/1 retry trigger.
			warm, _ := http.NewRequestWithContext(service.WithHTTPUpstreamProfile(t.Context(), service.HTTPUpstreamProfileOpenAI), http.MethodGet, server.URL+"/warm", nil)
			resp, err := up.Do(warm, "", 91, 1)
			require.NoError(t, err)
			_, _ = io.Copy(io.Discard, resp.Body)
			require.NoError(t, resp.Body.Close())
			ctx := service.WithAccountRPMHTTPAdmission(t.Context(), func(context.Context) error {
				t.Error("internal eval charged account RPM")
				return &service.AccountRPMError{}
			})
			_, record, err := svc.RunOpenAIEvalSampleAttempts(ctx, target, "OK", "", 3)
			if mode == "drop" {
				require.Error(t, err)
				require.Equal(t, 3, record.Attempts)
				require.EqualValues(t, 3, sends.Load())
			} else {
				if mode == "success" {
					require.NoError(t, err)
				} else {
					require.Error(t, err)
				}
				require.EqualValues(t, 1, sends.Load())
			}
			require.Zero(t, redirected.Load())
		})
	}
}

// This TLS HTTP/2 peer rejects fully transmitted streams, so counters describe
// physical requests rather than RoundTrip invocations or partially written bodies.
func r13H2RejectPeer(t *testing.T, mode string, linked bool) (string, *x509.Certificate, *atomic.Int64, *atomic.Int64) {
	t.Helper()
	certServer := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	cert := certServer.Certificate()
	config := certServer.TLS.Clone()
	config.NextProtos = []string{"h2"}
	certServer.Close()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", config)
	require.NoError(t, err)
	var sends, ticketSends atomic.Int64
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
				if _, err := io.ReadFull(reader, preface); err != nil {
					return
				}
				fr := http2.NewFramer(conn, reader)
				fr.ReadMetaHeaders = hpack.NewDecoder(4096, nil)
				if fr.WriteSettings() != nil {
					return
				}
				for {
					frame, err := fr.ReadFrame()
					if err != nil {
						return
					}
					var stream uint32
					switch f := frame.(type) {
					case *http2.SettingsFrame:
						if !f.IsAck() {
							_ = fr.WriteSettingsAck()
						}
						continue
					case *http2.MetaHeadersFrame:
						sends.Add(1)
						for _, h := range f.Fields {
							if h.Name == "x-codex-turn-state" && h.Value == "synthetic-ticket" {
								ticketSends.Add(1)
							}
						}
						if !f.StreamEnded() {
							continue
						}
						stream = f.StreamID
					case *http2.DataFrame:
						if !f.StreamEnded() {
							continue
						}
						stream = f.StreamID
					default:
						continue
					}
					if linked && sends.Load() == 1 {
						var block bytes.Buffer
						enc := hpack.NewEncoder(&block)
						_ = enc.WriteField(hpack.HeaderField{Name: ":status", Value: "200"})
						_ = enc.WriteField(hpack.HeaderField{Name: "x-codex-turn-state", Value: "synthetic-ticket"})
						_ = enc.WriteField(hpack.HeaderField{Name: "content-type", Value: "text/event-stream"})
						_ = fr.WriteHeaders(http2.HeadersFrameParam{StreamID: stream, BlockFragment: block.Bytes(), EndHeaders: true})
						_ = fr.WriteData(stream, true, []byte("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"model\":\"gpt-6-astra\"}}\n\n"))
						continue
					}
					if mode == "goaway" {
						_ = fr.WriteGoAway(0, http2.ErrCodeNo, nil)
						return
					}
					_ = fr.WriteRSTStream(stream, http2.ErrCodeRefusedStream)
				}
			}()
		}
	}()
	t.Cleanup(func() { _ = ln.Close(); wg.Wait() })
	return "https://" + ln.Addr().String(), cert, &sends, &ticketSends
}

func TestR13EvalHTTP2PhysicalMaximumAndLinkedStateProbe(t *testing.T) {
	for _, mode := range []string{"refused_stream", "goaway"} {
		for _, linked := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "/eval", true: "/linked"}[linked], func(t *testing.T) {
				url, cert, sends, tickets := r13H2RejectPeer(t, mode, linked)
				_, svc, target := r13EvalProductionUpstream(t, url, cert, true)
				if linked {
					target.Account.Type = service.AccountTypeOAuth
					target.Account.Credentials["access_token"] = "synthetic"
					probe := svc.RunOpenAIStateProbe(t.Context(), target)
					require.NotEmpty(t, probe.Failure)
					require.Equal(t, 2, probe.RequestCount)
					require.EqualValues(t, 2, sends.Load())
					require.EqualValues(t, 1, tickets.Load(), "linked ticket must never replay")
				} else {
					_, record, err := svc.RunOpenAIEvalSampleAttempts(t.Context(), target, "OK", "", 3)
					require.Error(t, err)
					require.Equal(t, 3, record.Attempts, "failure: %v", err)
					require.EqualValues(t, 3, sends.Load())
				}
			})
		}
	}
}

func r13CONNECTProxy(t *testing.T, destination string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var connects atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			w.WriteHeader(405)
			return
		}
		connects.Add(1)
		up, err := net.DialTimeout("tcp", destination, time.Second)
		if err != nil {
			w.WriteHeader(502)
			return
		}
		down, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			_ = up.Close()
			return
		}
		defer down.Close()
		defer up.Close()
		_, _ = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = buffer.Flush()
		done := make(chan struct{})
		go func() { _, _ = io.Copy(up, buffer); _ = up.Close(); close(done) }()
		_, _ = io.Copy(down, up)
		_ = down.Close()
		<-done
	}))
	t.Cleanup(server.Close)
	return server, &connects
}

func TestR13SingleSendCONNECTAndTLSFingerprint(t *testing.T) {
	// Child-only trust configuration avoids the process-wide SystemCertPool cache
	// and leaves all production certificate verification enabled.
	if target := os.Getenv("R13_LOCAL_TLS_TARGET"); target != "" {
		for _, eval := range []bool{false, true} {
			var admissions atomic.Int64
			ctx := service.WithAccountRPMHTTPAdmission(t.Context(), func(context.Context) error { admissions.Add(1); return nil })
			if eval {
				ctx = service.WithHTTPUpstreamSingleSend(ctx)
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader("synthetic"))
			require.NoError(t, err)
			resp, err := NewHTTPUpstream(nil).DoWithTLS(req, os.Getenv("R13_LOCAL_PROXY"), 93, 1, &tlsfingerprint.Profile{Name: "synthetic-local"})
			require.NoError(t, err)
			require.Equal(t, 200, resp.StatusCode)
			require.NoError(t, resp.Body.Close())
			if eval {
				require.Zero(t, admissions.Load(), "internal single-send does not charge RPM")
			} else {
				require.EqualValues(t, 1, admissions.Load())
			}
		}
		return
	}
	var sends atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		sends.Add(1)
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()
	proxy, connects := r13CONNECTProxy(t, server.Listener.Addr().String())
	parsed, err := url.Parse(proxy.URL)
	require.NoError(t, err)
	tr, err := buildUpstreamTransport(http2KeepAliveTestPoolSettings(), parsed, upstreamProtocolModeOpenAIH1)
	require.NoError(t, err)
	defer tr.CloseIdleConnections()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	ctx := service.WithAccountRPMHTTPAdmission(t.Context(), func(context.Context) error { return nil })
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, strings.NewReader("synthetic"))
	require.NoError(t, err)
	resp, err := httpClientWithAccountRPMAdmission(&http.Client{Transport: tr}).Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.EqualValues(t, 1, connects.Load())
	require.EqualValues(t, 1, sends.Load())
	certPath := filepath.Join(t.TempDir(), "local-ca.pem")
	require.NoError(t, os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600))
	childCtx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	child := exec.CommandContext(childCtx, os.Args[0], "-test.run=^TestR13SingleSendCONNECTAndTLSFingerprint$", "-test.count=1")
	child.Env = append(os.Environ(), "SSL_CERT_FILE="+certPath, "R13_LOCAL_TLS_TARGET="+server.URL, "R13_LOCAL_PROXY="+proxy.URL)
	out, err := child.CombinedOutput()
	require.NoError(t, err, "%s", out)
	require.EqualValues(t, 3, connects.Load())
	require.EqualValues(t, 3, sends.Load(), "real uTLS CONNECT limited and eval requests reached the local TLS peer")
}
