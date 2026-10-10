package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
)

type OpenAIEvalBackgroundControl struct {
	AccountID                int64                        `json:"account_id"`
	PausedUntil              *time.Time                   `json:"paused_until,omitempty"`
	PauseReason              string                       `json:"pause_reason,omitempty"`
	BudgetEnabled            bool                         `json:"budget_enabled"`
	MaxRequestsPerHour       int                          `json:"max_requests_per_hour"`
	MinSendIntervalSeconds   int                          `json:"min_send_interval_seconds"`
	MaxBackgroundConcurrency int                          `json:"max_background_concurrency"`
	SamplingWindowSeconds    int                          `json:"sampling_window_seconds"`
	Runtime                  *OpenAIEvalBackgroundRuntime `json:"runtime,omitempty"`
	RuntimeUnavailableReason string                       `json:"runtime_unavailable_reason,omitempty"`
}

type OpenAIEvalBackgroundRuntime struct {
	SentLastHour   int        `json:"sent_last_hour"`
	Reserved       int        `json:"reserved"`
	ActiveRuns     int        `json:"active_runs"`
	DeferredReason string     `json:"deferred_reason,omitempty"`
	NextSendAt     *time.Time `json:"next_send_at,omitempty"`
}

// Optional on GatewayCache: no change to existing mocks or gateway admission.
type OpenAIEvalBudgetStore interface {
	OpenAIEvalBudget(context.Context, string, OpenAIEvalBudgetOperation) (OpenAIEvalBudgetDecision, error)
}
type OpenAIEvalBudgetOperation struct {
	Action, Owner, TestType string
	SendID                  string
	RPM                     int
	Automatic               bool
	Nominal                 int
	Control                 OpenAIEvalBackgroundControl
}
type OpenAIEvalBudgetDecision struct {
	Allowed bool
	OpenAIEvalBackgroundRuntime
}

func normalizeOpenAIEvalBackgroundControl(c *OpenAIEvalBackgroundControl) error {
	c.Runtime = nil
	c.RuntimeUnavailableReason = ""
	c.PauseReason = strings.TrimSpace(c.PauseReason)
	if c.AccountID <= 0 {
		return errors.New("background control requires an account_id")
	}
	if len(c.PauseReason) > 500 {
		return errors.New("background pause reason exceeds 500 bytes")
	}
	if c.BudgetEnabled {
		if c.MaxRequestsPerHour == 0 {
			c.MaxRequestsPerHour = 60
		}
		if c.MinSendIntervalSeconds == 0 {
			c.MinSendIntervalSeconds = 60
		}
		if c.MaxBackgroundConcurrency == 0 {
			c.MaxBackgroundConcurrency = 1
		}
		if c.SamplingWindowSeconds == 0 {
			c.SamplingWindowSeconds = 900
		}
	}
	if c.MaxRequestsPerHour < 0 || c.MaxRequestsPerHour > 1000000 || c.MinSendIntervalSeconds < 0 || c.MinSendIntervalSeconds > 86400 || c.MaxBackgroundConcurrency < 0 || c.MaxBackgroundConcurrency > 1000 || c.SamplingWindowSeconds < 0 || c.SamplingWindowSeconds > 86400 {
		return errors.New("background control numeric limits are out of range")
	}
	return nil
}

func openAIEvalCredentialNamespace(a *Account) string {
	if a == nil {
		return ""
	}
	raw := ""
	if a.IsOpenAIOAuthLike() {
		// Account-level pressure is deliberately broader than identity policy's
		// user namespace: a duplicate missing user metadata must not escape it.
		if id := strings.TrimSpace(a.GetChatGPTAccountID()); id != "" {
			raw = "chatgpt:" + id
		}
	}
	// Credential material is hashed here and never used in a cache key or diagnostic.
	if raw == "" {
		token := a.GetCredential("api_key")
		if a.IsOpenAIOAuthLike() {
			token = a.GetOpenAIAccessToken()
		}
		if token == "" {
			return ""
		}
		raw = a.Platform + ":" + a.Type + ":" + token
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte("eval-credential-v1:"+raw)))
}

func (s *OpenAIEvalService) backgroundNamespace(ctx context.Context, id int64) (string, error) {
	credential, err := s.backgroundCredential(ctx, id)
	if err != nil {
		return "", err
	}
	return openAIEvalCredentialNamespace(credential), nil
}

func (s *OpenAIEvalService) backgroundCredential(ctx context.Context, id int64) (*Account, error) {
	if s.accounts == nil {
		return nil, errors.New("background account lookup unavailable")
	}
	account, err := s.accounts.GetByID(ctx, id)
	if err != nil || account == nil {
		return nil, errors.New("background account lookup failed")
	}
	credential, err := resolveCredentialAccount(ctx, s.accounts, account)
	if err != nil {
		return nil, errors.New("background credential lookup failed")
	}
	credential, err = openAIEvalCanonicalCredential(ctx, s.accounts, credential)
	if err != nil {
		return nil, err
	}
	key := openAIEvalCredentialNamespace(credential)
	if key == "" {
		return nil, errors.New("background credential namespace unavailable")
	}
	copy := *credential
	copy.Credentials = make(map[string]any, len(credential.Credentials))
	for k, v := range credential.Credentials {
		copy.Credentials[k] = v
	}
	return &copy, nil
}

func sameOpenAIEvalCredential(a, b *Account) bool {
	if a == nil || b == nil || a.Platform != b.Platform {
		return false
	}
	if key := openAIEvalCredentialNamespace(a); key != "" && key == openAIEvalCredentialNamespace(b) {
		return true
	}
	if a.IsOpenAIOAuthLike() && b.IsOpenAIOAuthLike() {
		for _, field := range []string{"access_token", "refresh_token"} {
			if token := a.GetCredential(field); token != "" && token == b.GetCredential(field) {
				return true
			}
		}
	}
	return false
}

// Fill a duplicate's missing principal metadata from a row with the same
// actual credential. Seeds and incomplete user metadata never split budgets.
func openAIEvalCanonicalCredential(ctx context.Context, repo AccountRepository, a *Account) (*Account, error) {
	rows, err := repo.ListAllWithFilters(ctx, a.Platform, "", "", "", 0, "")
	if err != nil {
		return nil, errors.New("background credential index unavailable")
	}
	canonical := a
	for i := range rows {
		candidate, err := resolveCredentialAccount(ctx, repo, &rows[i])
		if err != nil {
			return nil, errors.New("background credential lookup failed")
		}
		if !sameOpenAIEvalCredential(a, candidate) {
			continue
		}
		if id := strings.TrimSpace(candidate.GetChatGPTAccountID()); id != "" {
			if previous := strings.TrimSpace(canonical.GetChatGPTAccountID()); previous != "" && previous != id {
				return nil, errors.New("conflicting actual credential metadata")
			}
			canonical = candidate
		}
	}
	return canonical, nil
}

func (s *OpenAIEvalService) configuredBackgroundNamespace(ctx context.Context, id int64) (string, error) {
	if s.accounts == nil {
		return "", errors.New("background account lookup unavailable")
	}
	a, err := s.accounts.GetByID(ctx, id)
	if err != nil || a == nil {
		return "", errors.New("background account lookup failed")
	}
	credential, err := resolveCredentialAccount(ctx, s.accounts, a)
	if err != nil {
		return "", errors.New("background credential lookup failed")
	}
	credential, err = openAIEvalCanonicalCredential(ctx, s.accounts, credential)
	if err != nil {
		return "", err
	}
	if credential.IsOpenAIOAuth() && strings.TrimSpace(credential.GetChatGPTAccountID()) == "" {
		return "", errors.New("background controls require stable ChatGPT account metadata")
	}
	return openAIEvalCredentialNamespace(credential), nil
}

// Include unconfigured imports and shadows when yielding to foreground work.
// Resolve current repository rows on every automatic slot attempt, not a
// process-local credential index which can miss another instance's imports.
func (s *AccountTestService) openAIEvalCredentialPeers(ctx context.Context, a *Account) ([]AccountWithConcurrency, error) {
	if s.accountRepo == nil {
		return nil, errors.New("background account lookup unavailable")
	}
	credential, err := resolveCredentialAccount(ctx, s.accountRepo, a)
	if err != nil {
		return nil, err
	}
	ns := openAIEvalCredentialNamespace(credential)
	if ns == "" {
		return nil, errors.New("background credential namespace unavailable")
	}
	rows, err := s.accountRepo.ListAllWithFilters(ctx, a.Platform, "", "", "", 0, "")
	if err != nil {
		return nil, err
	}
	peers := map[int64]int{a.ID: a.Concurrency}
	for i := range rows {
		row := &rows[i]
		actual, err := resolveCredentialAccount(ctx, s.accountRepo, row)
		if err != nil {
			return nil, err
		}
		if openAIEvalCredentialNamespace(actual) == ns || sameOpenAIEvalCredential(credential, actual) {
			peers[row.ID] = row.Concurrency
		}
	}
	result := make([]AccountWithConcurrency, 0, len(peers))
	for id, limit := range peers {
		result = append(result, AccountWithConcurrency{ID: id, MaxConcurrency: limit})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func sameOpenAIEvalControls(a, b OpenAIEvalBackgroundControl) bool {
	a.AccountID, b.AccountID = 0, 0
	a.Runtime, b.Runtime = nil, nil
	a.RuntimeUnavailableReason, b.RuntimeUnavailableReason = "", ""
	// Time locations may differ after JSON decoding.
	if a.PausedUntil != nil && b.PausedUntil != nil && a.PausedUntil.Equal(*b.PausedUntil) {
		a.PausedUntil = b.PausedUntil
	}
	return reflect.DeepEqual(a, b)
}

func (s *OpenAIEvalService) validateBackgroundControls(ctx context.Context, cfg *OpenAIEvalConfig) error {
	seen := map[int64]bool{}
	namespaces := map[string]OpenAIEvalBackgroundControl{}
	for i := range cfg.BackgroundControls {
		c := &cfg.BackgroundControls[i]
		if err := normalizeOpenAIEvalBackgroundControl(c); err != nil {
			return err
		}
		if seen[c.AccountID] {
			return errors.New("duplicate background control account_id")
		}
		seen[c.AccountID] = true
		if s.accounts == nil {
			return errors.New("background account lookup unavailable")
		}
		key, err := s.configuredBackgroundNamespace(ctx, c.AccountID)
		if err != nil {
			return err
		}
		if other, ok := namespaces[key]; ok && !sameOpenAIEvalControls(other, *c) {
			return errors.New("conflicting background controls for shared credentials")
		}
		namespaces[key] = *c
	}
	return nil
}

// Resolve every configured row, even when the current duplicate has no control.
func (s *OpenAIEvalService) backgroundControl(ctx context.Context, key string) (OpenAIEvalBackgroundControl, bool, error) {
	cfg, err := s.repo.GetConfig(ctx)
	if err != nil {
		return OpenAIEvalBackgroundControl{}, false, err
	}
	var result OpenAIEvalBackgroundControl
	found := false
	if cfg == nil {
		return result, false, nil
	}
	copy := *cfg
	cfg = &copy
	if err := s.pruneBackgroundControls(ctx, cfg, nil); err != nil {
		return result, false, err
	}
	for _, c := range cfg.BackgroundControls {
		if err := normalizeOpenAIEvalBackgroundControl(&c); err != nil {
			return result, false, err
		}
		ns, err := s.configuredBackgroundNamespace(ctx, c.AccountID)
		if err != nil {
			return result, false, err
		}
		if ns != key {
			continue
		}
		if found && !sameOpenAIEvalControls(result, c) {
			return result, false, errors.New("conflicting background controls for shared credentials")
		}
		result, found = c, true
	}
	return result, found, nil
}

// Reload shared controls in one bounded query. Canonical credential discovery
// belongs to admission, not the once-per-second send-interval polling loop.
func (s *OpenAIEvalService) backgroundControlForRun(ctx context.Context, credential *Account) (OpenAIEvalBackgroundControl, bool, error) {
	cfg, err := s.repo.GetConfig(ctx)
	if err != nil {
		return OpenAIEvalBackgroundControl{}, false, err
	}
	if cfg == nil {
		return OpenAIEvalBackgroundControl{}, false, nil
	}
	var result OpenAIEvalBackgroundControl
	found := false
	ids := make([]int64, 0, len(cfg.BackgroundControls))
	for _, control := range cfg.BackgroundControls {
		ids = append(ids, control.AccountID)
	}
	accounts := make(map[int64]*Account, len(ids))
	if len(ids) > 0 {
		rows, err := s.accounts.GetByIDs(ctx, ids)
		if err != nil {
			return result, false, errors.New("background control account lookup failed")
		}
		for _, row := range rows {
			if row != nil {
				accounts[row.ID] = row
			}
		}
	}
	for _, c := range cfg.BackgroundControls {
		if err := normalizeOpenAIEvalBackgroundControl(&c); err != nil {
			return result, false, err
		}
		account := accounts[c.AccountID]
		if account == nil {
			continue
		}
		actual, err := resolveCredentialAccount(ctx, s.accounts, account)
		if err != nil {
			return result, false, errors.New("background control credential lookup failed")
		}
		if !sameOpenAIEvalCredential(credential, actual) {
			continue
		}
		if id := strings.TrimSpace(actual.GetChatGPTAccountID()); id != "" && id != strings.TrimSpace(credential.GetChatGPTAccountID()) {
			return result, false, errors.New("conflicting actual credential metadata")
		}
		if found && !sameOpenAIEvalControls(result, c) {
			return result, false, errors.New("conflicting background controls for shared credentials")
		}
		result, found = c, true
	}
	return result, found, nil
}

func (s *OpenAIEvalService) pruneBackgroundControls(ctx context.Context, cfg *OpenAIEvalConfig, allowed map[int64]bool) error {
	kept := make([]OpenAIEvalBackgroundControl, 0, len(cfg.BackgroundControls))
	for _, c := range cfg.BackgroundControls {
		if s.accounts != nil && (allowed == nil || allowed[c.AccountID]) {
			a, err := s.accounts.GetByID(ctx, c.AccountID)
			if errors.Is(err, ErrAccountNotFound) || (err == nil && a == nil) {
				continue
			}
			if err != nil {
				return fmt.Errorf("validate background account %d: %w", c.AccountID, err)
			}
		}
		kept = append(kept, c)
	}
	cfg.BackgroundControls = kept
	return nil
}

func (s *OpenAIEvalService) budgetStore() OpenAIEvalBudgetStore {
	if s.accountTest == nil || s.accountTest.openaiGatewayService == nil {
		return nil
	}
	store, _ := s.accountTest.openaiGatewayService.cache.(OpenAIEvalBudgetStore)
	return store
}

func (s *OpenAIEvalService) projectBackgroundRuntime(ctx context.Context, cfg *OpenAIEvalConfig) error {
	cfg.BackgroundControls = append([]OpenAIEvalBackgroundControl{}, cfg.BackgroundControls...)
	for i := range cfg.BackgroundControls {
		c := &cfg.BackgroundControls[i]
		c.Runtime = nil
		c.RuntimeUnavailableReason = ""
		if err := normalizeOpenAIEvalBackgroundControl(c); err != nil {
			c.RuntimeUnavailableReason = "background_control_invalid"
			continue
		}
		if s.accounts == nil {
			c.RuntimeUnavailableReason = "credential_namespace_unavailable"
			continue
		}
		ns, err := s.configuredBackgroundNamespace(ctx, c.AccountID)
		if err != nil {
			c.RuntimeUnavailableReason = "credential_namespace_unavailable"
			continue
		}
		store := s.budgetStore()
		if store == nil {
			c.RuntimeUnavailableReason = "evaluation_budget_unavailable"
			continue
		}
		d, err := store.OpenAIEvalBudget(ctx, ns, OpenAIEvalBudgetOperation{Action: "read", Control: *c})
		if err != nil {
			c.RuntimeUnavailableReason = "evaluation_budget_unavailable"
			continue
		}
		c.Runtime = &d.OpenAIEvalBackgroundRuntime
		if c.PausedUntil != nil && time.Now().Before(*c.PausedUntil) {
			c.Runtime.DeferredReason = "automatic_evaluation_paused"
		}
	}
	return nil
}

func openAIEvalBudgetFeasible(c OpenAIEvalBackgroundControl, nominal, rpm int) bool {
	if !c.BudgetEnabled {
		return true
	}
	if nominal > c.MaxRequestsPerHour {
		return false
	}
	seconds := (nominal - 1) * c.MinSendIntervalSeconds
	if rpm > 0 {
		seconds = max(seconds, ((nominal-1)/rpm)*60)
	}
	return seconds < c.SamplingWindowSeconds
}

type openAIEvalControllerKey struct{}

func isOpenAIEvalDeferredCode(code string) bool {
	return strings.HasPrefix(code, "evaluation_") || strings.HasPrefix(code, "background_") || strings.HasPrefix(code, "foreground_") || strings.HasPrefix(code, "account_rpm_") || code == "account_scheduling_disabled" || code == "automatic_evaluation_paused" || code == "sampling_window_exhausted" || code == "credential_namespace_changed" || code == "concurrency_unavailable" || code == "concurrency_wait_timeout" || code == "rate_limit" || code == "rate_limit_exceeded" || code == "rate_limited" || code == "http_429"
}

type OpenAIEvalRunBudgetDiagnostics struct {
	Sent           int
	DeferredReason string
}
type openAIEvalController struct {
	service                    *OpenAIEvalService
	store                      OpenAIEvalBudgetStore
	namespace, owner, testType string
	accountID                  int64
	automatic                  bool
	deadline                   time.Time
	mu                         sync.Mutex
	diagnostics                OpenAIEvalRunBudgetDiagnostics
	slot                       chan struct{}
	initialControl             *OpenAIEvalBackgroundControl
	credential                 *Account
}

func (c *openAIEvalController) deny(reason string) error {
	c.mu.Lock()
	c.diagnostics.DeferredReason = reason
	c.mu.Unlock()
	return &OpenAIEvalRequestError{Code: reason, Message: "evaluation deferred: " + reason, Attempted: false}
}
func openAIEvalRunDiagnostics(ctx context.Context) OpenAIEvalRunBudgetDiagnostics {
	c, _ := ctx.Value(openAIEvalControllerKey{}).(*openAIEvalController)
	if c == nil {
		return OpenAIEvalRunBudgetDiagnostics{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.diagnostics
}

func (c *openAIEvalController) current(ctx context.Context) (OpenAIEvalBackgroundControl, error) {
	c.mu.Lock()
	stopped := c.diagnostics.DeferredReason
	c.mu.Unlock()
	if stopped != "" {
		return OpenAIEvalBackgroundControl{}, c.deny(stopped)
	}
	if !c.deadline.IsZero() && !time.Now().Before(c.deadline) {
		return OpenAIEvalBackgroundControl{}, c.deny("sampling_window_exhausted")
	}
	account, err := c.service.accounts.GetByID(ctx, c.accountID)
	if err != nil || account == nil {
		return OpenAIEvalBackgroundControl{}, c.deny("credential_namespace_changed")
	}
	actual, err := resolveCredentialAccount(ctx, c.service.accounts, account)
	if err != nil || !sameOpenAIEvalCredential(c.credential, actual) || (strings.TrimSpace(actual.GetChatGPTAccountID()) != "" && openAIEvalCredentialNamespace(actual) != c.namespace) {
		return OpenAIEvalBackgroundControl{}, c.deny("credential_namespace_changed")
	}
	control, configured, err := c.service.backgroundControlForRun(ctx, c.credential)
	if err != nil {
		return control, c.deny("background_control_unavailable")
	}
	if c.automatic && control.PausedUntil != nil && time.Now().Before(*control.PausedUntil) {
		return control, c.deny("automatic_evaluation_paused")
	}
	if c.initialControl != nil {
		before, after := *c.initialControl, control
		before.PausedUntil, after.PausedUntil = nil, nil
		before.PauseReason, after.PauseReason = "", ""
		if !sameOpenAIEvalControls(before, after) {
			return control, c.deny("background_control_changed")
		}
	}
	if configured && c.store == nil {
		return control, c.deny("evaluation_budget_unavailable")
	}
	return control, nil
}

// Serialize physical samples within a budgeted run, including ModelTrace's
// workers. Redis's leased run limit then also bounds physical concurrency.
// This local gate is not the distributed admission authority.
func openAIEvalAcquireSampleGate(ctx context.Context) (func(), error) {
	c, _ := ctx.Value(openAIEvalControllerKey{}).(*openAIEvalController)
	if c == nil {
		return func() {}, nil
	}
	control, err := c.current(ctx)
	if err != nil {
		return nil, err
	}
	if !control.BudgetEnabled && !c.automatic {
		return func() {}, nil
	}
	select {
	case c.slot <- struct{}{}:
		return func() { <-c.slot }, nil
	case <-ctx.Done():
		return nil, c.deny("sampling_window_exhausted")
	}
}

// Called before acquiring any normal concurrency slot. Interval waits hold no slot.
func openAIEvalWaitSendInterval(ctx context.Context) error {
	c, _ := ctx.Value(openAIEvalControllerKey{}).(*openAIEvalController)
	if c == nil {
		return nil
	}
	for {
		control, err := c.current(ctx)
		if err != nil {
			return err
		}
		if !control.BudgetEnabled || c.store == nil {
			return nil
		}
		d, err := c.store.OpenAIEvalBudget(ctx, c.namespace, OpenAIEvalBudgetOperation{Action: "read", Control: control})
		if err != nil {
			return c.deny("evaluation_budget_unavailable")
		}
		if d.NextSendAt == nil || !time.Now().Before(*d.NextSendAt) {
			return nil
		}
		timer := time.NewTimer(min(time.Until(*d.NextSendAt), time.Second))
		select {
		case <-ctx.Done():
			timer.Stop()
			return c.deny("evaluation_cancelled")
		case <-timer.C:
		}
	}
}

func openAIEvalBeforeSend(ctx context.Context) error {
	c, _ := ctx.Value(openAIEvalControllerKey{}).(*openAIEvalController)
	if c == nil {
		return nil
	}
	control, err := c.current(ctx)
	if err != nil {
		return err
	}
	if c.automatic {
		if err := c.service.accountTest.checkOpenAIEvalAutomaticAccount(ctx, &Account{ID: c.accountID}); err != nil {
			return c.deny("account_scheduling_disabled_or_cooling_down")
		}
		gateway := c.service.accountTest.openaiGatewayService
		if gateway != nil && gateway.concurrencyService != nil {
			a, err := c.service.accounts.GetByID(ctx, c.accountID)
			if err != nil || a == nil {
				return c.deny("foreground_priority_unavailable")
			}
			peers, err := c.service.accountTest.openAIEvalCredentialPeers(ctx, a)
			if err != nil {
				return c.deny("foreground_priority_unavailable")
			}
			for _, peer := range peers {
				n, err := gateway.concurrencyService.cache.GetAccountWaitingCount(ctx, peer.ID)
				if err != nil {
					return c.deny("foreground_priority_unavailable")
				}
				if n > 0 {
					return c.deny("foreground_waiting")
				}
			}
		}
	}
	pending, _ := ctx.Value(openAIEvalPhysicalSendKey{}).(*openAIEvalPhysicalSend)
	if pending != nil && pending.prepared.Load() {
		return c.deny("evaluation_duplicate_send_admission")
	}
	if c.store != nil {
		if pending == nil {
			return c.deny("evaluation_send_observer_unavailable")
		}
		d, err := c.store.OpenAIEvalBudget(ctx, c.namespace, OpenAIEvalBudgetOperation{Action: "prepare", Owner: c.owner, Control: control, SendID: pending.id})
		if err != nil {
			return c.deny("evaluation_budget_unavailable")
		}
		if !d.Allowed {
			return c.deny(d.DeferredReason)
		}
	}
	if pending != nil {
		pending.prepared.Store(true)
	}
	return nil
}

func (s *OpenAIEvalService) beginBackgroundRun(ctx context.Context, target *OpenAIEvalTarget, request OpenAIEvalRunRequest, source, owner string, nominal int) (context.Context, func(), error) {
	credential, namespaceErr := s.backgroundCredential(ctx, request.AccountID)
	if namespaceErr != nil {
		return ctx, nil, namespaceErr
	}
	ns := openAIEvalCredentialNamespace(credential)
	// Legacy synthetic test doubles without credentials can still run with no controls.
	cfg, err := s.repo.GetConfig(ctx)
	if err != nil {
		return ctx, nil, err
	}
	if ns == "" && (cfg == nil || len(cfg.BackgroundControls) == 0) {
		return ctx, func() {}, nil
	}
	if ns == "" {
		return ctx, nil, errors.New("background credential namespace unavailable")
	}
	c := &openAIEvalController{service: s, store: s.budgetStore(), namespace: ns, credential: credential, owner: owner, testType: request.TestType, accountID: request.AccountID, automatic: source == "scheduled", slot: make(chan struct{}, 1)}
	control, err := c.current(ctx)
	if err != nil {
		return ctx, nil, err
	}

	if target.Account.Platform == PlatformPrism {
		return ctx, nil, errors.New("Prism channel has been removed")
	}
	rpm, err := target.Account.AccountRPMLimit()
	if err != nil {
		return ctx, nil, err
	}
	if !openAIEvalBudgetFeasible(control, nominal, rpm) {
		return ctx, nil, c.deny("budget_cannot_complete_run")
	}
	c.initialControl = &control
	if control.BudgetEnabled {
		c.deadline = time.Now().Add(time.Duration(control.SamplingWindowSeconds) * time.Second)
	}
	if c.store != nil {
		d, err := c.store.OpenAIEvalBudget(ctx, ns, OpenAIEvalBudgetOperation{Action: "admit", Owner: owner, TestType: request.TestType, Automatic: c.automatic, Nominal: nominal, Control: control, RPM: rpm})
		if err != nil {
			return ctx, nil, c.deny("evaluation_budget_unavailable")
		}
		if !d.Allowed {
			return ctx, nil, c.deny(d.DeferredReason)
		}
	}
	windowCtx, cancelWindow := context.WithCancel(ctx)
	if !c.deadline.IsZero() {
		cancelWindow()
		windowCtx, cancelWindow = context.WithDeadline(ctx, c.deadline)
	}
	runCtx := context.WithValue(windowCtx, openAIEvalControllerKey{}, c)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-runCtx.Done():
				return
			case <-ticker.C:
				if c.store != nil {
					renewCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
					d, err := c.store.OpenAIEvalBudget(renewCtx, ns, OpenAIEvalBudgetOperation{Action: "renew", Owner: owner})
					cancel()
					if err != nil || !d.Allowed {
						_ = c.deny("evaluation_budget_lease_lost")
						return
					}
				}
			}
		}
	}()
	return runCtx, func() {
		cancelWindow()
		close(stop)
		<-done
		if c.store != nil {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = c.store.OpenAIEvalBudget(cleanup, ns, OpenAIEvalBudgetOperation{Action: "release", Owner: owner})
		}
	}, nil
}
