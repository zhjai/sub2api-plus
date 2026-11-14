package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const ExcelBPSSessionProxyEnabledKey = "openai_excel_bps_session_proxy_enabled"
const ExcelBPSSessionProxyIDsKey = "openai_excel_bps_session_proxy_ids"

type excelBPSProxyNode struct {
	generation uint64
	revoked    bool
	proxy      Proxy
	ready      bool
	probing    bool
	active     int
	failures   int
	cooldown   time.Time
	checked    time.Time
}
type excelBPSProxyBinding struct {
	id      int64
	touched time.Time
	active  int
}
type excelBPSProxyPool struct {
	mu       sync.Mutex
	nodes    map[int64]*excelBPSProxyNode
	bindings map[string]*excelBPSProxyBinding
}
type excelBPSProxyLease struct {
	pool *excelBPSProxyPool
	id   int64
	key  string
	once sync.Once
}

func excelBPSProxyIDs(a *Account) ([]int64, bool) {
	var values []any
	switch raw := a.Extra[ExcelBPSSessionProxyIDsKey].(type) {
	case []any:
		values = raw
	case []int64:
		for _, id := range raw {
			values = append(values, id)
		}
	case nil:
		return nil, true
	default:
		return nil, false
	}
	seen := map[int64]bool{}
	ids := make([]int64, 0, len(values))
	for _, raw := range values {
		id, ok := bpsInteger(raw)
		if !ok || id <= 0 {
			return nil, false
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, true
}
func ValidateExcelBPSSessionProxyOptions(a *Account) error {
	if a == nil {
		return nil
	}
	if value, exists := a.Extra[ExcelBPSSessionProxyEnabledKey]; exists {
		if _, ok := value.(bool); !ok {
			return infraerrors.BadRequest("OPENAI_BPS_INVALID_OPTIONS", "BPS session proxy enabled must be a boolean")
		}
	}
	ids, ok := excelBPSProxyIDs(a)
	if !ok || len(ids) > 128 {
		return infraerrors.BadRequest("OPENAI_BPS_INVALID_OPTIONS", "BPS session proxy IDs must contain at most128 positive integer proxy IDs")
	}
	if a.Extra[ExcelBPSSessionProxyEnabledKey] == true && len(ids) == 0 {
		return infraerrors.BadRequest("OPENAI_BPS_PROXY_POOL_EMPTY", "Choose at least one proxy before enabling BPS session proxy")
	}
	return nil
}
func (s *OpenAIGatewayService) excelBPSProxyPoolFor(a *Account) *excelBPSProxyPool {
	pool := &excelBPSProxyPool{nodes: map[int64]*excelBPSProxyNode{}, bindings: map[string]*excelBPSProxyBinding{}}
	actual, _ := s.excelBPSProxyPools.LoadOrStore(a.ID, pool)
	return actual.(*excelBPSProxyPool)
}

// The parent and its children share a session root for egress, while their
// replay/catalog scopes remain distinct. Only a digest is retained in leases.
func excelBPSProxySessionKey(c *gin.Context, body []byte, accountID int64) string {
	root := ""
	if c != nil && c.Request != nil {
		root = strings.TrimSpace(c.GetHeader("session-id"))
		if root == "" {
			root = strings.TrimSpace(c.GetHeader("conversation-id"))
		}
	}
	if root == "" {
		root = gjson.GetBytes(body, "client_metadata.session_id").String()
	}
	if root == "" {
		return ""
	}
	h := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s", accountID, getAPIKeyIDFromContext(c), root)))
	return hex.EncodeToString(h[:])
}

func (s *OpenAIGatewayService) acquireExcelBPSSessionProxy(ctx context.Context, c *gin.Context, a *Account, body []byte) (*Account, *excelBPSProxyLease, error) {
	if a == nil || a.Extra[ExcelBPSSessionProxyEnabledKey] != true {
		return a, nil, nil
	}
	if err := ValidateExcelBPSSessionProxyOptions(a); err != nil {
		return nil, nil, err
	}
	if s.settingService == nil || s.settingService.proxyRepo == nil {
		return nil, nil, infraerrors.New(503, "basispoints_proxy_unavailable", "BPS session proxy pool is not configured")
	}
	pool := s.excelBPSProxyPoolFor(a)
	key := excelBPSProxySessionKey(c, body, a.ID)
	now := time.Now()
	pool.mu.Lock()
	defer pool.mu.Unlock()
	ids, _ := excelBPSProxyIDs(a)
	allowed := map[int64]bool{}
	for _, id := range ids {
		allowed[id] = true
	}
	for k, binding := range pool.bindings {
		if binding.active == 0 && now.Sub(binding.touched) >= 30*time.Minute {
			delete(pool.bindings, k)
		}
	}
	var selected *excelBPSProxyNode
	if binding := pool.bindings[key]; key != "" && binding != nil {
		node := pool.nodes[binding.id]
		if node != nil && allowed[binding.id] && node.ready && node.cooldown.Before(now) && node.proxy.IsActive() && !node.proxy.IsExpired(now) {
			selected = node
		}
		if selected == nil && binding.active > 0 {
			return nil, nil, infraerrors.New(503, "basispoints_proxy_session_busy", "BPS egress change waits for the active session request to finish")
		}
	}
	if selected == nil {
		for _, id := range ids {
			node := pool.nodes[id]
			if node != nil && node.ready && node.cooldown.Before(now) && node.proxy.IsActive() && !node.proxy.IsExpired(now) && (selected == nil || node.active < selected.active) {
				selected = node
			}
		}
	}
	if selected == nil {
		return nil, nil, infraerrors.New(503, "basispoints_proxy_warming", "No ready BPS session proxy is available; background validation is pending")
	}
	selected.active++
	if key != "" {
		binding := pool.bindings[key]
		if binding == nil || binding.id != selected.proxy.ID {
			binding = &excelBPSProxyBinding{id: selected.proxy.ID}
			pool.bindings[key] = binding
		}
		binding.active++
		binding.touched = now
	}
	clone := *a
	proxy := selected.proxy
	clone.ProxyID = &proxy.ID
	clone.Proxy = &proxy
	return &clone, &excelBPSProxyLease{pool: pool, id: proxy.ID, key: key}, nil
}
func (l *excelBPSProxyLease) release(err error) {
	if l == nil {
		return
	}
	l.once.Do(func() {
		l.pool.mu.Lock()
		defer l.pool.mu.Unlock()
		now := time.Now()
		node := l.pool.nodes[l.id]
		if node != nil {
			if node.active > 0 {
				node.active--
			}
			if excelBPSProxyStreamFailure(err) {
				node.generation++
				node.probing = false
				node.ready = false
				node.failures++
				cooldown := 5 * time.Minute * time.Duration(1<<min(node.failures-1, 3))
				if cooldown > 30*time.Minute {
					cooldown = 30 * time.Minute
				}
				node.cooldown = now.Add(cooldown)
			}
		}
		if b := l.pool.bindings[l.key]; b != nil && b.id == l.id {
			if b.active > 0 {
				b.active--
			}
			b.touched = now
		}
	})
}
func excelBPSProxyStreamFailure(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var evaluation *OpenAIEvalRequestError
	if errors.As(err, &evaluation) && evaluation.Code == "evaluation_connection_not_sent" {
		return true
	}
	var network *net.OpError
	if errors.As(err, &network) && network.Op == "dial" {
		return true
	}
	return errors.Is(err, io.ErrUnexpectedEOF) || strings.Contains(err.Error(), "stream_terminated") || strings.Contains(err.Error(), "connection reset") || strings.Contains(err.Error(), "unexpected EOF")
}

// Warm only in the maintenance loop. User requests never test cold nodes or
// fall back to direct egress. No generation is used to test access.
func (s *OpenAIGatewayService) warmExcelBPSSessionProxies(ctx context.Context, a *Account) {
	if a == nil || (!a.IsExcelBPSEnabled() && !a.ExcelBPS403RecoveryPending()) || !a.Schedulable || a.Extra[ExcelBPSSessionProxyEnabledKey] != true || s.settingService == nil || s.settingService.proxyRepo == nil {
		return
	}
	ids, ok := excelBPSProxyIDs(a)
	if !ok || len(ids) == 0 {
		return
	}
	proxies, err := s.settingService.proxyRepo.ListByIDs(ctx, ids)
	if err != nil {
		return
	}
	grant := a.ExcelBPSCredentialAccount()
	if grant == nil {
		return
	}
	pool := s.excelBPSProxyPoolFor(a)
	// Reconcile the complete repository result before probing any node. Missing
	// rows are deletions, and inactive/expired rows revoke cached readiness.
	allowed := make(map[int64]bool, len(ids))
	for _, id := range ids {
		allowed[id] = true
	}
	current := make(map[int64]Proxy, len(proxies))
	now := time.Now()
	for _, proxy := range proxies {
		if allowed[proxy.ID] && proxy.IsActive() && !proxy.IsExpired(now) {
			current[proxy.ID] = proxy
		}
	}
	pool.mu.Lock()
	for id, node := range pool.nodes {
		proxy, exists := current[id]
		if !exists || node.proxy.URL() != proxy.URL() {
			node.revoked = true
			node.generation++
			node.ready = false
			node.probing = false
			// Keep the leased endpoint and counters until all sends drain.
			// A changed endpoint cannot admit even another same-root request.
			if !exists || node.active > 0 {
				continue
			}
			node.checked = time.Time{}
		}
		node.revoked = false
		node.proxy = proxy
	}
	for id, proxy := range current {
		if pool.nodes[id] == nil {
			pool.nodes[id] = &excelBPSProxyNode{proxy: proxy}
		}
	}
	pool.mu.Unlock()
	for _, proxy := range current {
		if ctx.Err() != nil {
			return
		}
		pool.mu.Lock()
		node := pool.nodes[proxy.ID]
		if node.revoked || node.proxy.URL() != proxy.URL() || !node.proxy.IsActive() || node.proxy.IsExpired(time.Now()) || node.probing || time.Now().Before(node.cooldown) || node.ready && time.Since(node.checked) < 5*time.Minute {
			pool.mu.Unlock()
			continue
		}
		node.probing = true
		generation := node.generation
		pool.mu.Unlock()
		probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		var release func()
		if s.concurrencyService != nil {
			slot, slotErr := s.concurrencyService.AcquireAccountSlot(probeCtx, a.ID, a.Concurrency)
			if slotErr != nil || slot == nil || !slot.Acquired {
				pool.mu.Lock()
				if pool.nodes[proxy.ID] == node && node.generation == generation {
					node.probing = false
				}
				pool.mu.Unlock()
				cancel()
				continue
			}
			release = slot.ReleaseFunc
		}
		token, _, tokenErr := s.GetAccessToken(probeCtx, grant)
		ready := false
		if tokenErr == nil {
			req, reqErr := newExcelBPSRequest(probeCtx, nil, token, excelOAuthAccountID(token))
			if reqErr == nil {
				req.Method = http.MethodGet
				req.URL.Path = "/basispoints/api/responses/access"
				req.URL.RawQuery = "include_models=false"
				req.Body = nil
				req.GetBody = nil
				req.ContentLength = 0
				req.Header.Set("Accept", "application/json")
				projected := *grant
				projected.ProxyID = &proxy.ID
				projected.Proxy = &proxy
				resp, callErr := s.doAccountRPMUpstream(req, proxy.URL(), &projected)
				if callErr == nil && resp != nil && resp.Body != nil {
					var access struct {
						Allowed bool `json:"allowed"`
					}
					raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
					_ = resp.Body.Close()
					ready = resp.StatusCode == 200 && readErr == nil && json.Unmarshal(raw, &access) == nil && access.Allowed
				}
			}
		}
		if release != nil {
			release()
		}
		cancel()
		pool.mu.Lock()
		// Removal, endpoint replacement, or a lease failure invalidates the
		// in-flight probe; its old response must not resurrect readiness.
		if pool.nodes[proxy.ID] != node || node.generation != generation {
			pool.mu.Unlock()
			continue
		}
		node.probing = false
		node.checked = time.Now()
		node.ready = ready
		if ready {
			node.failures = 0
			node.cooldown = time.Time{}
		} else {
			node.cooldown = time.Now().Add(5 * time.Minute)
		}
		pool.mu.Unlock()
	}
}

func (s *OpenAIGatewayService) startExcelBPSMaintenance() {
	if s == nil || s.accountRepo == nil || s.httpUpstream == nil {
		return
	}
	s.excelBPSMaintenanceOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		s.excelBPSMaintenanceCancel = cancel
		s.excelBPSMaintenanceWorkers.Add(2)
		go func() {
			defer s.excelBPSMaintenanceWorkers.Done()
			s.RunExcelBPS403Recovery(ctx)
		}()
		go func() {
			defer s.excelBPSMaintenanceWorkers.Done()
			tick := time.NewTicker(30 * time.Second)
			defer tick.Stop()
			for {
				cycle, cancel := context.WithTimeout(ctx, 25*time.Second)
				accounts, err := s.accountRepo.ListByPlatform(cycle, PlatformOpenAI)
				if err == nil {
					for i := range accounts {
						s.warmExcelBPSSessionProxies(cycle, &accounts[i])
					}
				}
				cancel()
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
				}
			}
		}()
	})
}
