package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/gin-gonic/gin"
)

type openAILegacySessionHashContextKey struct{}

var openAILegacySessionHashKey = openAILegacySessionHashContextKey{}

var (
	openAIStickyLegacyReadFallbackTotal atomic.Int64
	openAIStickyLegacyReadFallbackHit   atomic.Int64
	openAIStickyLegacyDualWriteTotal    atomic.Int64
)

const openAISessionEscapeTTL = 30 * time.Minute

func openAISessionEscapeRouteLocalKey(groupID int64, sessionHash, requestedModel, requestedEffort string) string {
	dimensions := strings.ToLower(strings.TrimSpace(requestedModel)) + "\x00" + strings.ToLower(strings.TrimSpace(requestedEffort))
	digest := sha256.Sum256([]byte(dimensions))
	return fmt.Sprintf("%d:%s:%s", groupID, strings.TrimSpace(sessionHash), hex.EncodeToString(digest[:8]))
}

func openAIStickyCompatStats() (legacyReadFallbackTotal, legacyReadFallbackHit, legacyDualWriteTotal int64) {
	return openAIStickyLegacyReadFallbackTotal.Load(),
		openAIStickyLegacyReadFallbackHit.Load(),
		openAIStickyLegacyDualWriteTotal.Load()
}

// DeriveSessionHashFromSeed computes the current-format sticky-session hash
// from an arbitrary seed string.
func DeriveSessionHashFromSeed(seed string) string {
	currentHash, _ := deriveOpenAISessionHashes(seed)
	return currentHash
}

func deriveOpenAISessionHashes(sessionID string) (currentHash string, legacyHash string) {
	normalized := strings.TrimSpace(sessionID)
	if normalized == "" {
		return "", ""
	}

	currentHash = fmt.Sprintf("%016x", xxhash.Sum64String(normalized))
	sum := sha256.Sum256([]byte(normalized))
	legacyHash = hex.EncodeToString(sum[:])
	return currentHash, legacyHash
}

func withOpenAILegacySessionHash(ctx context.Context, legacyHash string) context.Context {
	if ctx == nil {
		return nil
	}
	trimmed := strings.TrimSpace(legacyHash)
	if trimmed == "" {
		return ctx
	}
	return context.WithValue(ctx, openAILegacySessionHashKey, trimmed)
}

func openAILegacySessionHashFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(openAILegacySessionHashKey).(string)
	return strings.TrimSpace(value)
}

func attachOpenAILegacySessionHashToGin(c *gin.Context, legacyHash string) {
	if c == nil || c.Request == nil {
		return
	}
	c.Request = c.Request.WithContext(withOpenAILegacySessionHash(c.Request.Context(), legacyHash))
}

func (s *OpenAIGatewayService) openAISessionHashReadOldFallbackEnabled() bool {
	if s == nil || s.cfg == nil {
		return true
	}
	return s.cfg.Gateway.OpenAIWS.SessionHashReadOldFallback
}

func (s *OpenAIGatewayService) openAISessionHashDualWriteOldEnabled() bool {
	if s == nil || s.cfg == nil {
		return true
	}
	return s.cfg.Gateway.OpenAIWS.SessionHashDualWriteOld
}

func (s *OpenAIGatewayService) openAISessionCacheKey(sessionHash string) string {
	normalized := strings.TrimSpace(sessionHash)
	if normalized == "" {
		return ""
	}
	return "openai:" + normalized
}

func (s *OpenAIGatewayService) openAILegacySessionCacheKey(ctx context.Context, sessionHash string) string {
	legacyHash := openAILegacySessionHashFromContext(ctx)
	if legacyHash == "" {
		return ""
	}
	legacyKey := "openai:" + legacyHash
	if legacyKey == s.openAISessionCacheKey(sessionHash) {
		return ""
	}
	return legacyKey
}

func (s *OpenAIGatewayService) openAIStickyLegacyTTL(ttl time.Duration) time.Duration {
	legacyTTL := ttl
	if legacyTTL <= 0 {
		legacyTTL = openaiStickySessionTTL
	}
	if legacyTTL > 10*time.Minute {
		return 10 * time.Minute
	}
	return legacyTTL
}

func (s *OpenAIGatewayService) getStickySessionAccountID(ctx context.Context, groupID *int64, sessionHash string) (int64, error) {
	if s == nil || s.cache == nil {
		return 0, nil
	}

	primaryKey := s.openAISessionCacheKey(sessionHash)
	if primaryKey == "" {
		return 0, nil
	}

	accountID, err := s.cache.GetSessionAccountID(ctx, derefGroupID(groupID), primaryKey)
	if err == nil && accountID > 0 {
		return accountID, nil
	}
	if !s.openAISessionHashReadOldFallbackEnabled() {
		return accountID, err
	}

	legacyKey := s.openAILegacySessionCacheKey(ctx, sessionHash)
	if legacyKey == "" {
		return accountID, err
	}

	openAIStickyLegacyReadFallbackTotal.Add(1)
	legacyAccountID, legacyErr := s.cache.GetSessionAccountID(ctx, derefGroupID(groupID), legacyKey)
	if legacyErr == nil && legacyAccountID > 0 {
		openAIStickyLegacyReadFallbackHit.Add(1)
		return legacyAccountID, nil
	}
	return accountID, err
}

func (s *OpenAIGatewayService) setStickySessionAccountID(ctx context.Context, groupID *int64, sessionHash string, accountID int64, ttl time.Duration) error {
	if s == nil || s.cache == nil || accountID <= 0 {
		return nil
	}
	primaryKey := s.openAISessionCacheKey(sessionHash)
	if primaryKey == "" {
		return nil
	}

	if err := s.cache.SetSessionAccountID(ctx, derefGroupID(groupID), primaryKey, accountID, ttl); err != nil {
		return err
	}

	if !s.openAISessionHashDualWriteOldEnabled() {
		return nil
	}
	legacyKey := s.openAILegacySessionCacheKey(ctx, sessionHash)
	if legacyKey == "" {
		return nil
	}
	if err := s.cache.SetSessionAccountID(ctx, derefGroupID(groupID), legacyKey, accountID, s.openAIStickyLegacyTTL(ttl)); err != nil {
		return err
	}
	openAIStickyLegacyDualWriteTotal.Add(1)
	return nil
}

func (s *OpenAIGatewayService) refreshStickySessionTTL(ctx context.Context, groupID *int64, sessionHash string, ttl time.Duration) error {
	if s == nil || s.cache == nil {
		return nil
	}
	primaryKey := s.openAISessionCacheKey(sessionHash)
	if primaryKey == "" {
		return nil
	}

	err := s.cache.RefreshSessionTTL(ctx, derefGroupID(groupID), primaryKey, ttl)
	if !s.openAISessionHashReadOldFallbackEnabled() && !s.openAISessionHashDualWriteOldEnabled() {
		return err
	}

	legacyKey := s.openAILegacySessionCacheKey(ctx, sessionHash)
	if legacyKey != "" {
		_ = s.cache.RefreshSessionTTL(ctx, derefGroupID(groupID), legacyKey, s.openAIStickyLegacyTTL(ttl))
	}
	return err
}

func (s *OpenAIGatewayService) deleteStickySessionAccountID(ctx context.Context, groupID *int64, sessionHash string) error {
	if s == nil || s.cache == nil {
		return nil
	}
	primaryKey := s.openAISessionCacheKey(sessionHash)
	if primaryKey == "" {
		return nil
	}

	err := s.cache.DeleteSessionAccountID(ctx, derefGroupID(groupID), primaryKey)
	if !s.openAISessionHashReadOldFallbackEnabled() && !s.openAISessionHashDualWriteOldEnabled() {
		return err
	}

	legacyKey := s.openAILegacySessionCacheKey(ctx, sessionHash)
	if legacyKey != "" {
		_ = s.cache.DeleteSessionAccountID(ctx, derefGroupID(groupID), legacyKey)
	}
	return err
}

// EscapeOpenAISessionAccount removes the sticky binding for a session only when
// it still points at failedAccountID.  A stream watchdog/transport failure is
// terminal for the current sampling, but the next sampling can safely migrate
// to another account.  The compare-before-delete guard prevents an older
// in-flight request from clearing a newer healthy binding.
func (s *OpenAIGatewayService) EscapeOpenAISessionAccount(ctx context.Context, groupID *int64, sessionHash string, failedAccountID int64) error {
	return s.escapeOpenAISessionAccount(ctx, groupID, sessionHash, "", "", failedAccountID, 0, false)
}

// EscapeOpenAISessionAccountForRequest records a session escape scoped to the
// client-requested model and reasoning effort. The next sampling can therefore
// move along that route's billing-rate ladder without excluding the account
// from unrelated model/effort traffic in the same conversation.
func (s *OpenAIGatewayService) EscapeOpenAISessionAccountForRequest(ctx context.Context, groupID *int64, sessionHash, requestedModel, requestedEffort string, failedAccountID int64, failedRateMultiplier float64) error {
	if strings.TrimSpace(requestedModel) == "" || math.IsNaN(failedRateMultiplier) || math.IsInf(failedRateMultiplier, 0) || failedRateMultiplier < 0 {
		return s.EscapeOpenAISessionAccount(ctx, groupID, sessionHash, failedAccountID)
	}
	return s.escapeOpenAISessionAccount(ctx, groupID, sessionHash, requestedModel, requestedEffort, failedAccountID, failedRateMultiplier, true)
}

func (s *OpenAIGatewayService) escapeOpenAISessionAccount(ctx context.Context, groupID *int64, sessionHash, requestedModel, requestedEffort string, failedAccountID int64, failedRateMultiplier float64, scoped bool) error {
	if s == nil || s.cache == nil || strings.TrimSpace(sessionHash) == "" || failedAccountID <= 0 {
		return nil
	}
	lookupCtx := context.WithoutCancel(ctx)
	var lookupErr error
	if !scoped {
		bound, err := s.getStickySessionAccountID(lookupCtx, groupID, sessionHash)
		if err != nil {
			if !errors.Is(err, ErrStickySessionNotFound) {
				lookupErr = err
			}
		} else if bound == failedAccountID {
			if deleteErr := s.deleteStickySessionAccountID(lookupCtx, groupID, sessionHash); deleteErr != nil {
				lookupErr = deleteErr
			}
		}
	}

	group := derefGroupID(groupID)
	session := strings.TrimSpace(sessionHash)
	key := fmt.Sprintf("%d:%s", group, session)
	if scoped {
		key = openAISessionEscapeRouteLocalKey(group, session, requestedModel, requestedEffort)
		if escapeCache, ok := s.cache.(OpenAISessionEscapeRouteCache); ok {
			if err := escapeCache.AddOpenAISessionEscapedRoute(lookupCtx, group, session, requestedModel, requestedEffort, failedAccountID, failedRateMultiplier, openAISessionEscapeTTL); err != nil {
				lookupErr = errors.Join(lookupErr, err)
			}
		}
	} else if escapeCache, ok := s.cache.(OpenAISessionEscapeCache); ok {
		if err := escapeCache.AddOpenAISessionEscapedAccount(lookupCtx, group, session, failedAccountID, openAISessionEscapeTTL); err != nil {
			lookupErr = errors.Join(lookupErr, err)
		}
	}
	s.openaiSessionEscapeMu.Lock()
	if s.openaiSessionEscapes == nil {
		s.openaiSessionEscapes = make(map[string]map[int64]time.Time)
	}
	if s.openaiSessionEscapes[key] == nil {
		s.openaiSessionEscapes[key] = make(map[int64]time.Time)
	}
	s.openaiSessionEscapes[key][failedAccountID] = time.Now().Add(openAISessionEscapeTTL)
	if scoped {
		if s.openaiSessionEscapeRates == nil {
			s.openaiSessionEscapeRates = make(map[string]float64)
		}
		if current, ok := s.openaiSessionEscapeRates[key]; !ok || failedRateMultiplier > current {
			s.openaiSessionEscapeRates[key] = failedRateMultiplier
		}
	}
	s.openaiSessionEscapeMu.Unlock()
	return lookupErr
}

// OpenAISessionEscapedAccountIDs returns hard-excluded accounts for the next
// sampling of a session. It intentionally returns a fresh map so callers may
// merge it into their request-local failover exclusions.
func (s *OpenAIGatewayService) OpenAISessionEscapedAccountIDs(groupID *int64, sessionHash string) map[int64]struct{} {
	if s == nil || strings.TrimSpace(sessionHash) == "" {
		return nil
	}
	key := fmt.Sprintf("%d:%s", derefGroupID(groupID), strings.TrimSpace(sessionHash))
	now := time.Now()
	s.openaiSessionEscapeMu.Lock()
	entries := s.openaiSessionEscapes[key]
	result := make(map[int64]struct{}, len(entries))
	for accountID, until := range entries {
		if !until.After(now) {
			delete(entries, accountID)
			continue
		}
		result[accountID] = struct{}{}
	}
	if len(entries) == 0 {
		delete(s.openaiSessionEscapes, key)
	}
	s.openaiSessionEscapeMu.Unlock()
	if escapeCache, ok := s.cache.(OpenAISessionEscapeCache); ok {
		if ids, err := escapeCache.GetOpenAISessionEscapedAccountIDs(context.Background(), derefGroupID(groupID), strings.TrimSpace(sessionHash)); err == nil {
			for _, accountID := range ids {
				if accountID > 0 {
					result[accountID] = struct{}{}
				}
			}
		}
	}
	return result
}

// OpenAISessionEscapeRouteStateForRequest returns the model/effort-scoped
// exclusion set and the highest failed billing rate remembered for the route.
func (s *OpenAIGatewayService) OpenAISessionEscapeRouteStateForRequest(groupID *int64, sessionHash, requestedModel, requestedEffort string) OpenAISessionEscapeRouteState {
	if s == nil || strings.TrimSpace(sessionHash) == "" || strings.TrimSpace(requestedModel) == "" {
		return OpenAISessionEscapeRouteState{}
	}
	group := derefGroupID(groupID)
	session := strings.TrimSpace(sessionHash)
	key := openAISessionEscapeRouteLocalKey(group, session, requestedModel, requestedEffort)
	now := time.Now()
	state := OpenAISessionEscapeRouteState{}
	s.openaiSessionEscapeMu.Lock()
	entries := s.openaiSessionEscapes[key]
	for accountID, until := range entries {
		if !until.After(now) {
			delete(entries, accountID)
			continue
		}
		state.AccountIDs = append(state.AccountIDs, accountID)
	}
	if len(entries) == 0 {
		delete(s.openaiSessionEscapes, key)
		delete(s.openaiSessionEscapeRates, key)
	}
	if rate, ok := s.openaiSessionEscapeRates[key]; ok {
		state.FailedRateMultiplier = rate
		state.HasFailedRate = true
	}
	s.openaiSessionEscapeMu.Unlock()

	if escapeCache, ok := s.cache.(OpenAISessionEscapeRouteCache); ok {
		if cached, err := escapeCache.GetOpenAISessionEscapedRoute(context.Background(), group, session, requestedModel, requestedEffort); err == nil {
			state.AccountIDs = append(state.AccountIDs, cached.AccountIDs...)
			if cached.HasFailedRate && (!state.HasFailedRate || cached.FailedRateMultiplier > state.FailedRateMultiplier) {
				state.FailedRateMultiplier = cached.FailedRateMultiplier
				state.HasFailedRate = true
			}
		}
	}
	if len(state.AccountIDs) > 1 {
		seen := make(map[int64]struct{}, len(state.AccountIDs))
		unique := state.AccountIDs[:0]
		for _, accountID := range state.AccountIDs {
			if accountID <= 0 {
				continue
			}
			if _, ok := seen[accountID]; ok {
				continue
			}
			seen[accountID] = struct{}{}
			unique = append(unique, accountID)
		}
		state.AccountIDs = unique
	}
	return state
}
