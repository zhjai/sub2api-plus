package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var (
	ErrOpenAIOpaqueRouteEpochBindingUnavailable = errors.New("opaque route epoch binding unavailable")
	ErrOpenAIOpaqueRouteEpochBindingLookup      = errors.New("opaque route epoch binding lookup failed")
)

const (
	openAIOpaqueRouteEpochContextKey = "openai_opaque_route_epoch_attempt"
	openAIOpaqueRouteEpochTTL        = time.Hour
	openAIOpaqueRouteEpochWindow     = time.Hour
	openAIOpaqueRouteEpochMaxBumps   = 3
	openAIOpaqueRouteEpochMaxEntries = 65536
)

const (
	OpenAIIntegritySignalModelMismatch = "response_model_mismatch"
	OpenAIIntegritySignalExecLeak      = "exec_protocol_leak"
	OpenAIIntegritySignalEmptyComplete = "empty_completed"
	OpenAIIntegritySignalSilentRefusal = "silent_refusal"
	OpenAIIntegritySignalStreamEnded   = "stream_terminated"
)

type openAIOpaqueRouteEpochLocalState struct {
	OpenAIOpaqueRouteEpochState
	Until time.Time
}

type openAIOpaqueRouteEpochAttempt struct {
	GroupID         int64
	SessionHash     string
	RequestedModel  string
	RequestedEffort string
	AccountID       int64
	Epoch           int64
	Bumps           int
	Apply           bool
}

func openAIOpaqueRouteEpochKey(groupID int64, sessionHash, requestedModel, requestedEffort string, accountID int64) string {
	dimensions := strings.ToLower(strings.TrimSpace(requestedModel)) + "\x00" + strings.ToLower(strings.TrimSpace(requestedEffort))
	digest := sha256.Sum256([]byte(dimensions))
	return fmt.Sprintf("%d:%s:%s:%d", groupID, strings.TrimSpace(sessionHash), hex.EncodeToString(digest[:8]), accountID)
}

func (s *OpenAIGatewayService) localOpenAIOpaqueRouteEpoch(key string, now time.Time) OpenAIOpaqueRouteEpochState {
	if s == nil || key == "" {
		return OpenAIOpaqueRouteEpochState{}
	}
	s.openaiOpaqueRouteEpochMu.Lock()
	defer s.openaiOpaqueRouteEpochMu.Unlock()
	entry, ok := s.openaiOpaqueRouteEpochs[key]
	if !ok || !entry.Until.After(now) {
		delete(s.openaiOpaqueRouteEpochs, key)
		return OpenAIOpaqueRouteEpochState{}
	}
	entry.Until = now.Add(openAIOpaqueRouteEpochTTL)
	s.openaiOpaqueRouteEpochs[key] = entry
	return entry.OpenAIOpaqueRouteEpochState
}

func cleanupOpenAIOpaqueRouteEpochs(entries map[string]openAIOpaqueRouteEpochLocalState, now time.Time, maxEntries int, incomingKey string) {
	for key, entry := range entries {
		if !entry.Until.After(now) {
			delete(entries, key)
		}
	}
	if _, exists := entries[incomingKey]; exists {
		return
	}
	for len(entries) >= maxEntries && maxEntries > 0 {
		for key := range entries {
			delete(entries, key)
			break
		}
	}
}

func (s *OpenAIGatewayService) storeLocalOpenAIOpaqueRouteEpoch(key string, state OpenAIOpaqueRouteEpochState, now time.Time) {
	if s == nil || key == "" {
		return
	}
	s.openaiOpaqueRouteEpochMu.Lock()
	defer s.openaiOpaqueRouteEpochMu.Unlock()
	if s.openaiOpaqueRouteEpochs == nil {
		s.openaiOpaqueRouteEpochs = make(map[string]openAIOpaqueRouteEpochLocalState)
	}
	cleanupOpenAIOpaqueRouteEpochs(s.openaiOpaqueRouteEpochs, now, openAIOpaqueRouteEpochMaxEntries, key)
	current := s.openaiOpaqueRouteEpochs[key]
	if current.Until.After(now) && current.Epoch > state.Epoch {
		state = current.OpenAIOpaqueRouteEpochState
	}
	s.openaiOpaqueRouteEpochs[key] = openAIOpaqueRouteEpochLocalState{
		OpenAIOpaqueRouteEpochState: state,
		Until:                       now.Add(openAIOpaqueRouteEpochTTL),
	}
}

func (s *OpenAIGatewayService) getOpenAIOpaqueRouteEpoch(
	ctx context.Context,
	groupID int64,
	sessionHash, requestedModel, requestedEffort string,
	accountID int64,
) OpenAIOpaqueRouteEpochState {
	if s == nil || strings.TrimSpace(sessionHash) == "" || strings.TrimSpace(requestedModel) == "" || accountID <= 0 {
		return OpenAIOpaqueRouteEpochState{}
	}
	now := time.Now()
	key := openAIOpaqueRouteEpochKey(groupID, sessionHash, requestedModel, requestedEffort, accountID)
	local := s.localOpenAIOpaqueRouteEpoch(key, now)
	if cache, ok := s.cache.(OpenAIOpaqueRouteEpochCache); ok {
		lookupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		cached, err := cache.GetOpenAIOpaqueRouteEpoch(lookupCtx, groupID, sessionHash, requestedModel, requestedEffort, accountID, openAIOpaqueRouteEpochTTL)
		cancel()
		if err == nil {
			if cached.Epoch < local.Epoch && local.Epoch > 0 {
				if converger, ok := s.cache.(OpenAIOpaqueRouteEpochConvergenceCache); ok {
					convergeCtx, convergeCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
					converged, convergeErr := converger.ConvergeOpenAIOpaqueRouteEpoch(
						convergeCtx, groupID, sessionHash, requestedModel, requestedEffort, accountID, local, openAIOpaqueRouteEpochTTL,
					)
					convergeCancel()
					if convergeErr == nil && converged.Epoch >= local.Epoch {
						local = converged
					}
				}
			} else if cached.Epoch >= local.Epoch {
				local = cached
			}
			if local.Epoch > 0 {
				s.storeLocalOpenAIOpaqueRouteEpoch(key, local, now)
			}
		}
	}
	return local
}

// SnapshotOpenAIOpaqueRouteEpoch reads the future-root route state without
// changing the identity or response bindings of an active connection.
func (s *OpenAIGatewayService) SnapshotOpenAIOpaqueRouteEpoch(
	ctx context.Context,
	groupID int64,
	sessionHash, requestedModel, requestedEffort string,
	accountID int64,
) OpenAIOpaqueRouteEpochState {
	return s.getOpenAIOpaqueRouteEpoch(ctx, groupID, sessionHash, requestedModel, requestedEffort, accountID)
}

// PrepareOpenAIOpaqueRouteEpochAttempt stages the identity epoch used by all
// HTTP and WebSocket request builders for the selected account. A continuation
// uses the exact epoch that created previousResponseID, even if the route has
// since rotated again.
func (s *OpenAIGatewayService) PrepareOpenAIOpaqueRouteEpochAttempt(
	ctx context.Context,
	c *gin.Context,
	groupID int64,
	sessionHash, requestedModel, requestedEffort string,
	account *Account,
	previousResponseID string,
) (OpenAIOpaqueRouteEpochState, error) {
	if c != nil {
		c.Set(openAIOpaqueRouteEpochContextKey, openAIOpaqueRouteEpochAttempt{})
	}
	if account == nil || !account.IsOpenAIOpaqueUpstream() {
		return OpenAIOpaqueRouteEpochState{}, nil
	}
	state := s.getOpenAIOpaqueRouteEpoch(ctx, groupID, sessionHash, requestedModel, requestedEffort, account.ID)
	applyEpoch := state.Epoch
	if responseID := strings.TrimSpace(previousResponseID); responseID != "" {
		store := s.getOpenAIWSStateStore()
		if store == nil {
			return state, fmt.Errorf("%w: response=%s", ErrOpenAIOpaqueRouteEpochBindingLookup, responseID)
		}
		boundEpoch, found, err := store.GetResponseRouteEpoch(ctx, groupID, responseID, account.ID)
		if err != nil {
			return state, fmt.Errorf("%w: response=%s: %w", ErrOpenAIOpaqueRouteEpochBindingLookup, responseID, err)
		}
		if found {
			applyEpoch = boundEpoch
		} else {
			return state, fmt.Errorf("%w: response=%s", ErrOpenAIOpaqueRouteEpochBindingUnavailable, responseID)
		}
	}
	if c != nil {
		c.Set(openAIOpaqueRouteEpochContextKey, openAIOpaqueRouteEpochAttempt{
			GroupID:         groupID,
			SessionHash:     strings.TrimSpace(sessionHash),
			RequestedModel:  strings.TrimSpace(requestedModel),
			RequestedEffort: strings.TrimSpace(requestedEffort),
			AccountID:       account.ID,
			Epoch:           applyEpoch,
			Bumps:           state.Bumps,
			Apply:           applyEpoch > 0,
		})
	}
	return state, nil
}

func (s *OpenAIGatewayService) bumpLocalOpenAIOpaqueRouteEpoch(key string, expectedEpoch int64, now time.Time) (OpenAIOpaqueRouteEpochState, bool) {
	if s == nil || key == "" {
		return OpenAIOpaqueRouteEpochState{}, false
	}
	s.openaiOpaqueRouteEpochMu.Lock()
	defer s.openaiOpaqueRouteEpochMu.Unlock()
	if s.openaiOpaqueRouteEpochs == nil {
		s.openaiOpaqueRouteEpochs = make(map[string]openAIOpaqueRouteEpochLocalState)
	}
	cleanupOpenAIOpaqueRouteEpochs(s.openaiOpaqueRouteEpochs, now, openAIOpaqueRouteEpochMaxEntries, key)
	entry := s.openaiOpaqueRouteEpochs[key]
	if !entry.Until.After(now) {
		entry = openAIOpaqueRouteEpochLocalState{}
	}
	state := entry.OpenAIOpaqueRouteEpochState
	if state.Epoch != expectedEpoch {
		entry.Until = now.Add(openAIOpaqueRouteEpochTTL)
		s.openaiOpaqueRouteEpochs[key] = entry
		return state, state.Epoch > expectedEpoch
	}
	windowStarted := time.Unix(state.WindowStartedUnix, 0)
	if state.WindowStartedUnix <= 0 || now.Sub(windowStarted) >= openAIOpaqueRouteEpochWindow {
		state.Bumps = 0
		state.WindowStartedUnix = now.Unix()
	}
	if state.Bumps >= openAIOpaqueRouteEpochMaxBumps {
		entry.OpenAIOpaqueRouteEpochState = state
		entry.Until = now.Add(openAIOpaqueRouteEpochTTL)
		s.openaiOpaqueRouteEpochs[key] = entry
		return state, false
	}
	state.Epoch++
	state.Bumps++
	entry.OpenAIOpaqueRouteEpochState = state
	entry.Until = now.Add(openAIOpaqueRouteEpochTTL)
	s.openaiOpaqueRouteEpochs[key] = entry
	return state, true
}

// BumpOpenAIOpaqueRouteEpoch atomically rotates the upstream-facing identity.
// A concurrent request that already advanced expectedEpoch is treated as an
// advance so callers can adopt that epoch without incrementing twice.
func (s *OpenAIGatewayService) BumpOpenAIOpaqueRouteEpoch(
	ctx context.Context,
	groupID int64,
	sessionHash, requestedModel, requestedEffort string,
	accountID, expectedEpoch int64,
) (OpenAIOpaqueRouteEpochState, bool, error) {
	if s == nil || strings.TrimSpace(sessionHash) == "" || strings.TrimSpace(requestedModel) == "" || accountID <= 0 {
		return OpenAIOpaqueRouteEpochState{}, false, nil
	}
	now := time.Now()
	key := openAIOpaqueRouteEpochKey(groupID, sessionHash, requestedModel, requestedEffort, accountID)
	if cache, ok := s.cache.(OpenAIOpaqueRouteEpochCache); ok {
		local := s.localOpenAIOpaqueRouteEpoch(key, now)
		if local.Epoch > 0 {
			if converger, ok := s.cache.(OpenAIOpaqueRouteEpochConvergenceCache); ok {
				convergeCtx, convergeCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
				_, _ = converger.ConvergeOpenAIOpaqueRouteEpoch(
					convergeCtx, groupID, sessionHash, requestedModel, requestedEffort, accountID, local, openAIOpaqueRouteEpochTTL,
				)
				convergeCancel()
			}
		}
		bumpCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		state, advanced, err := cache.BumpOpenAIOpaqueRouteEpoch(
			bumpCtx, groupID, sessionHash, requestedModel, requestedEffort, accountID, expectedEpoch,
			openAIOpaqueRouteEpochMaxBumps, openAIOpaqueRouteEpochWindow, openAIOpaqueRouteEpochTTL,
		)
		cancel()
		if err == nil {
			if state.Epoch < local.Epoch {
				return local, false, nil
			}
			s.storeLocalOpenAIOpaqueRouteEpoch(key, state, now)
			return state, advanced, nil
		}
		state, advanced = s.bumpLocalOpenAIOpaqueRouteEpoch(key, expectedEpoch, now)
		return state, advanced, err
	}
	state, advanced := s.bumpLocalOpenAIOpaqueRouteEpoch(key, expectedEpoch, now)
	return state, advanced, nil
}

func openAIOpaqueRouteEpochAttemptFromContext(c *gin.Context, account *Account) (openAIOpaqueRouteEpochAttempt, bool) {
	if c == nil || account == nil || !account.IsOpenAIOpaqueUpstream() {
		return openAIOpaqueRouteEpochAttempt{}, false
	}
	raw, exists := c.Get(openAIOpaqueRouteEpochContextKey)
	if !exists {
		return openAIOpaqueRouteEpochAttempt{}, false
	}
	attempt, ok := raw.(openAIOpaqueRouteEpochAttempt)
	return attempt, ok && attempt.Apply && attempt.AccountID == account.ID && attempt.Epoch > 0
}

// openAIOpaqueRouteEpochAffinity is an internal-only WebSocket pool key. It
// separates connections opened with different rotated upstream identities
// without exposing the route metadata to the upstream.
func openAIOpaqueRouteEpochAffinity(c *gin.Context, account *Account) string {
	attempt, ok := openAIOpaqueRouteEpochAttemptFromContext(c, account)
	if !ok {
		return ""
	}
	scope := fmt.Sprintf("%d\x00%s\x00%s\x00%s\x00%d\x00%d",
		attempt.GroupID,
		attempt.SessionHash,
		strings.ToLower(strings.TrimSpace(attempt.RequestedModel)),
		strings.ToLower(strings.TrimSpace(attempt.RequestedEffort)),
		attempt.AccountID,
		attempt.Epoch,
	)
	digest := sha256.Sum256([]byte(scope))
	return hex.EncodeToString(digest[:16])
}

func (s *OpenAIGatewayService) bindOpenAIResponseRouteEpoch(ctx context.Context, c *gin.Context, account *Account, responseID string) {
	if s == nil || account == nil || strings.TrimSpace(responseID) == "" {
		return
	}
	if c == nil || !account.IsOpenAIOpaqueUpstream() {
		return
	}
	// Epoch zero is the original upstream identity and is just as important
	// for continuation affinity as a rotated identity. Apply only controls
	// whether request headers/body need rewriting, not whether we bind it.
	raw, exists := c.Get(openAIOpaqueRouteEpochContextKey)
	attempt, ok := raw.(openAIOpaqueRouteEpochAttempt)
	if exists && (!ok || attempt.AccountID != account.ID || attempt.Epoch < 0) {
		return
	}
	// Direct service callers without an attempt send the original identity.
	// Record epoch zero for them as well as handler-prepared attempts.
	if err := s.getOpenAIWSStateStore().BindResponseRouteEpoch(
		ctx, getOpenAIGroupIDFromContext(c), responseID, account.ID, attempt.Epoch, s.openAIWSResponseStickyTTL(),
	); err != nil {
		logOpenAIWSModeInfo("response_route_epoch_bind_failed account_id=%d epoch=%d err=%v", account.ID, attempt.Epoch, err)
	}
}

// A live WebSocket cannot change its account or handshake identity to resume
// a historical response. Validate every later turn before any upstream write.
func (s *OpenAIGatewayService) validateOpenAIWSTurnContinuation(ctx context.Context, c *gin.Context, account *Account, payload []byte) error {
	previousResponseID := openAIWSPayloadStringFromRaw(payload, "previous_response_id")
	if previousResponseID == "" || account == nil || !account.IsOpenAI() {
		return nil
	}
	store := s.getOpenAIWSStateStore()
	groupID := getOpenAIGroupIDFromContext(c)
	ownerID, err := store.GetResponseAccount(ctx, groupID, previousResponseID)
	if err != nil {
		return NewOpenAIWSClientCloseError(coderws.StatusTryAgainLater, "previous_response_id owner is temporarily unavailable", err)
	}
	if ownerID <= 0 {
		return NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "previous_response_id owner is unavailable for this connection", nil)
	}
	if ownerID > 0 && ownerID != account.ID {
		if account.IsOpenAIOpaqueUpstream() {
			return NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "previous_response_id belongs to another account; please reconnect", nil)
		}
		owner, bound, ownerErr := s.ResolveOpenAIPreviousResponseOwner(ctx, &groupID, previousResponseID)
		if ownerErr != nil {
			return NewOpenAIWSClientCloseError(coderws.StatusTryAgainLater, "previous_response_id owner is temporarily unavailable", ownerErr)
		}
		if !bound || owner == nil || owner.IsOpenAIOpaqueUpstream() {
			return NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "previous_response_id owner is unavailable for this connection", nil)
		}
	}
	if !account.IsOpenAIOpaqueUpstream() {
		return nil
	}
	if ownerID != account.ID {
		return NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "previous_response_id owner is unavailable for this connection", nil)
	}
	boundEpoch, found, err := store.GetResponseRouteEpoch(ctx, groupID, previousResponseID, account.ID)
	if err != nil {
		return NewOpenAIWSClientCloseError(coderws.StatusTryAgainLater, "previous_response_id route is temporarily unavailable", err)
	}
	connectionEpoch := int64(0)
	if c != nil {
		if raw, exists := c.Get(openAIOpaqueRouteEpochContextKey); exists {
			attempt, ok := raw.(openAIOpaqueRouteEpochAttempt)
			if !ok || attempt.AccountID != account.ID {
				return NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "connection route identity is unavailable; please reconnect", nil)
			}
			connectionEpoch = attempt.Epoch
		}
	}
	if !found || boundEpoch != connectionEpoch {
		return NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "previous_response_id requires its original route; please reconnect", nil)
	}
	return nil
}

func openAIOpaqueRouteEpochValue(accountID, epoch int64, kind, raw string) string {
	raw = strings.TrimSpace(raw)
	if accountID <= 0 || epoch <= 0 || raw == "" {
		return raw
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("sub2api:route-epoch:v1|%d|%d|%s|%s", accountID, epoch, kind, raw)))
	return "r" + hex.EncodeToString(sum[:12])
}

func applyOpenAIOpaqueRouteEpochBody(c *gin.Context, account *Account, body []byte) ([]byte, bool, error) {
	attempt, ok := openAIOpaqueRouteEpochAttemptFromContext(c, account)
	if !ok || len(body) == 0 || !gjson.ValidBytes(body) {
		return body, false, nil
	}
	path := "prompt_cache_key"
	root := gjson.ParseBytes(body)
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(root.Get("type").String())), "response.") && root.Get("response").IsObject() {
		path = "response.prompt_cache_key"
	}
	raw := strings.TrimSpace(gjson.GetBytes(body, path).String())
	if raw == "" {
		return body, false, nil
	}
	next, err := sjson.SetBytes(body, path, openAIOpaqueRouteEpochValue(account.ID, attempt.Epoch, "prompt-cache", raw))
	if err != nil {
		return body, false, fmt.Errorf("rekey opaque upstream prompt_cache_key: %w", err)
	}
	return next, true, nil
}

func applyOpenAIOpaqueRouteEpochPayload(c *gin.Context, account *Account, payload map[string]any) bool {
	attempt, ok := openAIOpaqueRouteEpochAttemptFromContext(c, account)
	if !ok || payload == nil {
		return false
	}
	raw, _ := payload["prompt_cache_key"].(string)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	payload["prompt_cache_key"] = openAIOpaqueRouteEpochValue(account.ID, attempt.Epoch, "prompt-cache", raw)
	return true
}

var openAIOpaqueStickyHeaderKinds = []struct {
	name string
	kind string
}{
	{name: "session-id", kind: "session"},
	{name: "session_id", kind: "session"},
	{name: "conversation_id", kind: "conversation"},
	{name: "x-session-affinity", kind: "session"},
	{name: "x-session-id", kind: "session"},
	{name: "x-opencode-session", kind: "session"},
	{name: "x-conversation-id", kind: "conversation"},
}

func applyOpenAIOpaqueRouteEpochHeaders(c *gin.Context, account *Account, headers http.Header, bodyHasPromptCacheKey bool) bool {
	attempt, ok := openAIOpaqueRouteEpochAttemptFromContext(c, account)
	if !ok || headers == nil {
		return false
	}
	changed := false
	hasStickyHeader := false
	for _, field := range openAIOpaqueStickyHeaderKinds {
		raw := strings.TrimSpace(headers.Get(field.name))
		if raw == "" {
			continue
		}
		hasStickyHeader = true
		headers.Set(field.name, openAIOpaqueRouteEpochValue(account.ID, attempt.Epoch, field.kind, raw))
		changed = true
	}
	if !hasStickyHeader && !bodyHasPromptCacheKey && strings.TrimSpace(attempt.SessionHash) != "" {
		headers.Set("session_id", openAIOpaqueRouteEpochValue(account.ID, attempt.Epoch, "synthetic", attempt.SessionHash))
		changed = true
	}
	return changed
}
