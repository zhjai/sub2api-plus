package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func timezoneTestBody(t *testing.T, text string, marked bool) []byte {
	t.Helper()
	item := map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": text}}}
	if marked {
		item["internal_chat_message_metadata_passthrough"] = map[string]any{"content_item_kinds": []string{"environments.environment_context"}}
	}
	body, err := json.Marshal(map[string]any{"input": []any{item}, "previous_response_id": "resp_keep", "store": false})
	require.NoError(t, err)
	return body
}
func timezoneTestAccount(mode, zone string) *Account {
	return &Account{ID: 1, Platform: PlatformOpenAI, Extra: map[string]any{UpstreamClientTimezoneModeKey: mode, UpstreamClientTimezoneKey: zone}}
}
func TestUpstreamClientTimezoneManualProtocolOnly(t *testing.T) {
	r := newUpstreamTimezoneResolver()
	r.now = func() time.Time { return time.Date(2026, 7, 1, 1, 0, 0, 0, time.UTC) }
	account := timezoneTestAccount("manual", "America/Los_Angeles")
	env := "<environment_context>\n<cwd>/work/Asia/Shanghai</cwd><timezone> Asia/Shanghai </timezone><current_date>2026-07-01</current_date></environment_context>"
	body := timezoneTestBody(t, env, true)
	out, err := r.apply(context.Background(), account, body, "")
	require.NoError(t, err)
	text := gjson.GetBytes(out, "input.0.content.0.text").String()
	require.Contains(t, text, "<timezone> America/Los_Angeles </timezone>")
	require.Contains(t, text, "<current_date>2026-06-30</current_date>")
	require.Contains(t, text, "/work/Asia/Shanghai")
	require.Equal(t, "resp_keep", gjson.GetBytes(out, "previous_response_id").String())
	require.False(t, gjson.GetBytes(out, "store").Bool())
	for _, text := range []string{env, "请分析 Asia/Shanghai 和 America/New_York 的时差", "```xml\n" + env + "\n```"} {
		unmarked := timezoneTestBody(t, text, false)
		out, err = r.apply(context.Background(), account, unmarked, "")
		require.NoError(t, err)
		require.Equal(t, unmarked, out)
	}
	// Winter uses standard time; the same UTC instant is the prior local date.
	r.now = func() time.Time { return time.Date(2026, 1, 1, 7, 30, 0, 0, time.UTC) }
	out, err = r.apply(context.Background(), account, timezoneTestBody(t, strings.ReplaceAll(env, "2026-07-01", "2026-01-01"), true), "")
	require.NoError(t, err)
	require.Contains(t, gjson.GetBytes(out, "input.0.content.0.text").String(), "2025-12-31")
	historical := strings.ReplaceAll(env, "2026-07-01", "2025-10-12")
	out, err = r.apply(context.Background(), account, timezoneTestBody(t, historical, true), "")
	require.NoError(t, err)
	require.Contains(t, gjson.GetBytes(out, "input.0.content.0.text").String(), "2025-10-12")
}
func TestUpstreamClientTimezoneBadConfigAndMalformedCarrier(t *testing.T) {
	for _, extra := range []map[string]any{{UpstreamClientTimezoneModeKey: "manual"}, {UpstreamClientTimezoneModeKey: "auto_proxy"}, {UpstreamClientTimezoneKey: "Local"}, {UpstreamClientTimezoneKey: "../etc/passwd"}, {UpstreamClientTimezoneModeKey: 3}} {
		require.Error(t, ValidateUpstreamClientTimezoneExtra(extra))
	}
	require.NoError(t, ValidateUpstreamClientTimezoneExtra(map[string]any{UpstreamClientTimezoneModeKey: "manual", UpstreamClientTimezoneKey: "UTC"}))
	r := newUpstreamTimezoneResolver()
	for _, env := range []string{"<environment_context><timezone>Asia/Shanghai</timezone>", "<environment_context><timezone>UTC</timezone><timezone>UTC</timezone></environment_context>", "<environment_context><timezone><x>UTC</x></timezone></environment_context>"} {
		body := timezoneTestBody(t, env, true)
		out, err := r.apply(context.Background(), timezoneTestAccount("manual", "UTC"), body, "")
		require.Error(t, err)
		require.Equal(t, body, out)
		require.NotContains(t, err.Error(), "Asia/Shanghai")
	}
}
func TestUpstreamClientTimezoneWebMetadata(t *testing.T) {
	r := newUpstreamTimezoneResolver()
	body := []byte(`{"tools":[{"type":"web_search","user_location":{"timezone":"Asia/Shanghai","city":"Shanghai"}},{"type":"function","name":"timezone","timezone":"Asia/Shanghai"}],"metadata":{"timezone":"Asia/Shanghai"}}`)
	out, err := r.apply(context.Background(), timezoneTestAccount("manual", "UTC"), body, "")
	require.NoError(t, err)
	require.Equal(t, "UTC", gjson.GetBytes(out, "tools.0.user_location.timezone").String())
	require.Equal(t, "Shanghai", gjson.GetBytes(out, "tools.0.user_location.city").String())
	require.Equal(t, "Asia/Shanghai", gjson.GetBytes(out, "tools.1.timezone").String())
	require.Equal(t, "Asia/Shanghai", gjson.GetBytes(out, "metadata.timezone").String())
}
func TestUpstreamClientTimezoneAutoCacheRotationFailure(t *testing.T) {
	r := newUpstreamTimezoneResolver()
	now := time.Now()
	r.now = func() time.Time { return now }
	traces, geos := 0, 0
	exit := upstreamTimezoneExit{IP: "8.8.8.8", Country: "US"}
	probeFail, geoFail := false, false
	r.trace = func(_ context.Context, proxy string) (upstreamTimezoneExit, error) {
		traces++
		if probeFail {
			return upstreamTimezoneExit{}, errors.New("secret URL")
		}
		return exit, nil
	}
	r.geo = func(_ context.Context, proxy string, e upstreamTimezoneExit) (string, error) {
		geos++
		if geoFail {
			return "", errors.New("secret URL")
		}
		return "America/New_York", nil
	}
	state, err := r.resolve(context.Background(), "http://proxy:8000")
	require.NoError(t, err)
	require.Equal(t, "proxy_exit", state.Source)
	_, err = r.resolve(context.Background(), "http://proxy:8000")
	require.NoError(t, err)
	require.Equal(t, 1, traces)
	require.Equal(t, 1, geos)
	now = now.Add(6 * time.Minute)
	_, err = r.resolve(context.Background(), "http://proxy:8000")
	require.NoError(t, err)
	require.Equal(t, 2, traces)
	require.Equal(t, 1, geos)
	probeFail = true
	now = now.Add(6 * time.Minute)
	state, err = r.resolve(context.Background(), "http://proxy:8000")
	require.NoError(t, err)
	require.Equal(t, "stale", state.Status)
	probeFail = false
	geoFail = true
	exit.IP = "1.1.1.1"
	now = now.Add(2 * time.Minute)
	state, err = r.resolve(context.Background(), "http://proxy:8000")
	require.Error(t, err)
	require.Empty(t, state.Timezone)
	require.NotContains(t, err.Error(), "secret")
	geoFail = false
	state, err = r.resolve(context.Background(), "")
	require.NoError(t, err)
	require.Equal(t, "server_exit", state.Source)
	// A different selected proxy never reuses a route's failed/backoff result.
	state, err = r.resolve(context.Background(), "http://other:8000")
	require.NoError(t, err)
	require.Equal(t, "proxy_exit", state.Source)
}
func TestUpstreamClientTimezoneNoCarrierNoProbeAndSingleflight(t *testing.T) {
	r := newUpstreamTimezoneResolver()
	var calls atomic.Int32
	r.trace = func(context.Context, string) (upstreamTimezoneExit, error) {
		calls.Add(1)
		time.Sleep(20 * time.Millisecond)
		return upstreamTimezoneExit{IP: "8.8.8.8"}, nil
	}
	r.geo = func(context.Context, string, upstreamTimezoneExit) (string, error) { return "UTC", nil }
	ordinary := timezoneTestBody(t, "timezone Asia/Shanghai", false)
	out, err := r.apply(context.Background(), timezoneTestAccount("auto", ""), ordinary, "")
	require.NoError(t, err)
	require.Equal(t, ordinary, out)
	require.Zero(t, calls.Load())
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := r.resolve(context.Background(), "")
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	require.Equal(t, int32(1), calls.Load())
}
func TestUpstreamClientTimezoneProbeTransportLocalOnly(t *testing.T) {
	var hits atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		require.Equal(t, "example.invalid", r.URL.Host)
		require.Empty(t, r.Header.Get("Authorization"))
		require.Empty(t, r.Header.Get("Cookie"))
		require.Empty(t, r.Header.Get("X-Client-Timezone"))
		w.Write([]byte("local mock"))
	}))
	defer proxy.Close()
	body, err := upstreamTimezoneGET(context.Background(), proxy.URL, "http://example.invalid/trace")
	require.NoError(t, err)
	require.Equal(t, "local mock", string(body))
	require.Equal(t, int32(1), hits.Load())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("direct")) }))
	defer server.Close()
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	body, err = upstreamTimezoneGET(context.Background(), "", server.URL)
	require.NoError(t, err)
	require.Equal(t, "direct", string(body))
}

func TestUpstreamClientTimezoneAutoFailsClosedOnlyForCarrier(t *testing.T) {
	r := newUpstreamTimezoneResolver()
	r.trace = func(context.Context, string) (upstreamTimezoneExit, error) {
		return upstreamTimezoneExit{}, errors.New("must never expose secret source")
	}
	body := timezoneTestBody(t, "<environment_context><timezone>Asia/Shanghai</timezone></environment_context>", true)
	out, err := r.apply(context.Background(), timezoneTestAccount("auto", ""), body, "")
	require.Error(t, err)
	require.Equal(t, body, out)
	var typed *UpstreamClientTimezoneError
	require.ErrorAs(t, err, &typed)
	require.Equal(t, "exit_probe_failed", typed.Code)
	state, ok := r.status(timezoneTestAccount("auto", ""))
	require.True(t, ok)
	require.Equal(t, "error", state.Status)
	require.Equal(t, "server_exit", state.Source)
	require.NotContains(t, err.Error(), "secret")
	require.NotContains(t, err.Error(), "Asia/Shanghai")
	off, err := r.apply(context.Background(), timezoneTestAccount("off", ""), body, "")
	require.NoError(t, err)
	require.Equal(t, body, off)
}
func TestUpstreamClientTimezoneStatusUsesProxyValueNotPointer(t *testing.T) {
	r := newUpstreamTimezoneResolver()
	first := timezoneTestAccount("auto", "")
	proxyID := int64(42)
	first.ProxyID = &proxyID
	r.record(first, UpstreamClientTimezoneState{Mode: "auto", Timezone: "UTC", Status: "ready"})
	second := timezoneTestAccount("auto", "")
	otherPointer := int64(42)
	second.ProxyID = &otherPointer
	_, ok := r.status(second)
	require.True(t, ok)
	otherPointer = 43
	_, ok = r.status(second)
	require.False(t, ok)
}
