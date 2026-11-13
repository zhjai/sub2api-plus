package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyurl"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyutil"
	"golang.org/x/sync/singleflight"
)

const upstreamTimezoneRouteTTL = 5 * time.Minute
const upstreamTimezoneGeoTTL = 12 * time.Hour
const upstreamTimezoneRetryTTL = time.Minute
const upstreamTimezoneCacheLimit = 1024

type upstreamTimezoneExit struct{ IP, Country string }
type upstreamTimezoneRoute struct {
	State UpstreamClientTimezoneState
	Exit  upstreamTimezoneExit
	Next  time.Time
}
type upstreamTimezoneGeo struct {
	Zone string
	At   time.Time
}
type upstreamTimezoneAccountState struct {
	Signature string
	State     UpstreamClientTimezoneState
}
type upstreamTimezoneResolver struct {
	mu       sync.Mutex
	flights  singleflight.Group
	routes   map[string]upstreamTimezoneRoute
	zones    map[upstreamTimezoneExit]upstreamTimezoneGeo
	accounts map[int64]upstreamTimezoneAccountState
	now      func() time.Time
	// Tests inject these two operations; production only issues credential-free GETs.
	trace func(context.Context, string) (upstreamTimezoneExit, error)
	geo   func(context.Context, string, upstreamTimezoneExit) (string, error)
}

func newUpstreamTimezoneResolver() *upstreamTimezoneResolver {
	return &upstreamTimezoneResolver{routes: make(map[string]upstreamTimezoneRoute), zones: make(map[upstreamTimezoneExit]upstreamTimezoneGeo), accounts: make(map[int64]upstreamTimezoneAccountState), now: time.Now, trace: probeUpstreamTimezoneExit, geo: lookupUpstreamTimezone}
}
func upstreamTimezoneRouteKey(proxy string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(proxy)))
}
func upstreamTimezoneAccountSignature(a *Account) string {
	mode, zone := upstreamTimezoneConfig(a)
	proxy := ""
	if a.Proxy != nil {
		proxy = a.Proxy.URL()
	}
	proxyID := int64(0)
	if a.ProxyID != nil {
		proxyID = *a.ProxyID
	}
	return upstreamTimezoneRouteKey(fmt.Sprintf("%s\x00%s\x00%d\x00%s", mode, zone, proxyID, proxy))
}
func (r *upstreamTimezoneResolver) record(a *Account, state UpstreamClientTimezoneState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.accounts) >= upstreamTimezoneCacheLimit {
		clear(r.accounts)
	}
	r.accounts[a.ID] = upstreamTimezoneAccountState{Signature: upstreamTimezoneAccountSignature(a), State: state}
}
func (r *upstreamTimezoneResolver) status(a *Account) (UpstreamClientTimezoneState, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cached, ok := r.accounts[a.ID]
	return cached.State, ok && cached.Signature == upstreamTimezoneAccountSignature(a)
}
func timezoneRouteResult(route upstreamTimezoneRoute) (UpstreamClientTimezoneState, error) {
	if route.State.Timezone == "" {
		return route.State, &UpstreamClientTimezoneError{Code: route.State.Error}
	}
	return route.State, nil
}
func (r *upstreamTimezoneResolver) resolve(ctx context.Context, proxy string) (UpstreamClientTimezoneState, error) {
	normalized, _, err := proxyurl.Parse(proxy)
	if err != nil {
		return UpstreamClientTimezoneState{Mode: "auto", Status: "error"}, &UpstreamClientTimezoneError{Code: "invalid_proxy"}
	}
	key := upstreamTimezoneRouteKey(normalized)
	r.mu.Lock()
	cached, ok := r.routes[key]
	r.mu.Unlock()
	if ok && r.now().Before(cached.Next) {
		return timezoneRouteResult(cached)
	}
	result := r.flights.DoChan(key, func() (any, error) {
		r.mu.Lock()
		old, exists := r.routes[key]
		r.mu.Unlock()
		now := r.now()
		if exists && now.Before(old.Next) {
			return old, nil
		}
		probeCtx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		source := "server_exit"
		if normalized != "" {
			source = "proxy_exit"
		}
		next := upstreamTimezoneRoute{State: UpstreamClientTimezoneState{Mode: "auto", Status: "error", Source: source}, Next: now.Add(upstreamTimezoneRetryTTL)}
		exit, traceErr := r.trace(probeCtx, normalized)
		if traceErr != nil {
			next.State.Error = "exit_probe_failed"
			// Keep a bounded last-good only for the same configured egress route.
			if old.State.UpdatedAt != nil && now.Sub(*old.State.UpdatedAt) < upstreamTimezoneGeoTTL {
				next.State.Timezone = old.State.Timezone
				next.State.UpdatedAt = old.State.UpdatedAt
				next.Exit = old.Exit
			}
		} else {
			next.Exit = exit
			r.mu.Lock()
			known, found := r.zones[exit]
			r.mu.Unlock()
			if !found || now.Sub(known.At) >= upstreamTimezoneGeoTTL {
				zone, geoErr := r.geo(probeCtx, normalized, exit)
				if geoErr == nil {
					_, geoErr = loadUpstreamTimezone(zone)
				}
				if geoErr == nil {
					known = upstreamTimezoneGeo{Zone: zone, At: now}
					found = true
					r.mu.Lock()
					if len(r.zones) >= upstreamTimezoneCacheLimit {
						clear(r.zones)
					}
					r.zones[exit] = known
					r.mu.Unlock()
				} else {
					next.State.Error = "exit_timezone_lookup_failed"
					// Never reuse a different exit's timezone after a proxy rotates.
					if !found || now.Sub(known.At) >= upstreamTimezoneGeoTTL {
						found = false
					}
				}
			}
			if found {
				next.State.Timezone = known.Zone
				at := known.At
				next.State.UpdatedAt = &at
			}
			if next.State.Error == "" {
				next.State.Status = "ready"
				next.Next = now.Add(upstreamTimezoneRouteTTL)
			}
		}
		if next.State.Error != "" && next.State.Timezone != "" {
			next.State.Status = "stale"
		}
		r.mu.Lock()
		if len(r.routes) >= upstreamTimezoneCacheLimit {
			clear(r.routes)
		}
		r.routes[key] = next
		r.mu.Unlock()
		return next, nil
	})
	select {
	case <-ctx.Done():
		return UpstreamClientTimezoneState{Mode: "auto", Status: "error", Error: "resolution_cancelled"}, &UpstreamClientTimezoneError{Code: "resolution_cancelled"}
	case result := <-result:
		return timezoneRouteResult(result.Val.(upstreamTimezoneRoute))
	}
}

// The resolver uses its own transport: no account bearer token, cookies, client
// headers or environment proxy inheritance. Redirects cannot move a probe off-host.
func upstreamTimezoneGET(ctx context.Context, proxy, endpoint string) ([]byte, error) {
	_, parsed, err := proxyurl.Parse(proxy)
	if err != nil {
		return nil, errors.New("invalid proxy")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	if err := proxyutil.ConfigureTransportProxy(transport, parsed); err != nil {
		return nil, errors.New("invalid proxy")
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, errors.New("invalid probe endpoint")
	}
	req.Header.Set("Accept", "application/json, text/plain")
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("exit probe transport failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("exit probe rejected")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if err != nil || len(body) > 65536 {
		return nil, errors.New("invalid probe body")
	}
	return body, nil
}
func probeUpstreamTimezoneExit(ctx context.Context, proxy string) (upstreamTimezoneExit, error) {
	body, err := upstreamTimezoneGET(ctx, proxy, "https://chatgpt.com/cdn-cgi/trace")
	if err != nil {
		return upstreamTimezoneExit{}, err
	}
	fields := map[string]string{}
	for _, line := range strings.Split(string(body), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			fields[k] = v
		}
	}
	ip, err := netip.ParseAddr(fields["ip"])
	if err != nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || fields["h"] != "chatgpt.com" {
		return upstreamTimezoneExit{}, errors.New("invalid exit trace")
	}
	country := fields["loc"]
	if len(country) != 2 || country == "XX" || country == "T1" {
		country = ""
	}
	return upstreamTimezoneExit{IP: ip.String(), Country: country}, nil
}
func lookupUpstreamTimezone(ctx context.Context, proxy string, exit upstreamTimezoneExit) (string, error) {
	body, err := upstreamTimezoneGET(ctx, proxy, "https://ipwho.is/"+exit.IP)
	if err != nil {
		return "", err
	}
	var data struct {
		Success  *bool  `json:"success"`
		IP       string `json:"ip"`
		Country  string `json:"country_code"`
		Timezone struct {
			ID string `json:"id"`
		} `json:"timezone"`
	}
	if json.Unmarshal(body, &data) != nil || data.Success == nil || !*data.Success || data.IP != exit.IP || (exit.Country != "" && exit.Country != data.Country) {
		return "", errors.New("invalid exit geolocation")
	}
	if _, err := loadUpstreamTimezone(data.Timezone.ID); err != nil {
		return "", errors.New("invalid exit timezone")
	}
	return data.Timezone.ID, nil
}
