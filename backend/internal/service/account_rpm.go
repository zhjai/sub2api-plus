package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

const AccountRPMLimitMax = 10000

// Account RPM is scoped to the selected local account ID, including shadows.
// It is independent of credentials, groups and the legacy Anthropic base_rpm.
func (a *Account) AccountRPMLimit() (int, error) {
	if a == nil {
		return 0, nil
	}
	return parseAccountRPMLimit(a.Extra)
}

func parseAccountRPMLimit(extra map[string]any) (int, error) {
	v, present := extra["rpm_limit"]
	if !present {
		return 0, nil
	}
	var n int64
	switch value := v.(type) {
	case int:
		n = int64(value)
	case int64:
		n = value
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) || value != math.Trunc(value) || value < 0 || value > AccountRPMLimitMax {
			return 0, invalidAccountRPMLimit()
		}
		n = int64(value)
	case json.Number:
		var err error
		n, err = value.Int64()
		if err != nil {
			return 0, invalidAccountRPMLimit()
		}
	default:
		return 0, invalidAccountRPMLimit()
	}
	if n < 0 || n > AccountRPMLimitMax {
		return 0, invalidAccountRPMLimit()
	}
	return int(n), nil
}

func invalidAccountRPMLimit() error {
	return infraerrors.BadRequest("INVALID_ACCOUNT_RPM_LIMIT", "extra.rpm_limit must be an integer between 0 and 10000")
}

func ValidateAccountRPMExtra(extra map[string]any) error {
	_, err := parseAccountRPMLimit(extra)
	return err
}

type AccountRPMDecision struct {
	Allowed    bool
	Used       int
	ResetAt    time.Time
	RetryAfter time.Duration
}

// Optional capability: broad GatewayCache implementations and mocks stay compatible.
type AccountRPMAdmissionCache interface {
	AdmitAccountRPM(context.Context, int64, int, string) (AccountRPMDecision, error)
	ReadAccountRPMBatch(context.Context, map[int64]int) (map[int64]AccountRPMDecision, error)
}

type AccountRPMCapacity struct {
	Status    string     `json:"status"`
	Used      *int       `json:"used"`
	Remaining *int       `json:"remaining"`
	ResetAt   *time.Time `json:"reset_at"`
}

type AccountRPMError struct {
	AccountID   int64
	Limit       int
	Decision    AccountRPMDecision
	Unavailable bool
	NoMigration bool
	Cause       error
}

func (e *AccountRPMError) Error() string {
	if e.Unavailable {
		return "account RPM admission unavailable"
	}
	return "account request rate limit reached"
}
func (e *AccountRPMError) Unwrap() error { return e.Cause }
func (e *AccountRPMError) StatusCode() int {
	if e.Unavailable {
		return http.StatusServiceUnavailable
	}
	return http.StatusTooManyRequests
}
func (e *AccountRPMError) Code() string {
	if e.Unavailable {
		return "account_rpm_unavailable"
	}
	return "account_rpm_limit_exceeded"
}
func (e *AccountRPMError) RetryAfterSeconds() int {
	n := int(math.Ceil(e.Decision.RetryAfter.Seconds()))
	if n < 1 {
		return 1
	}
	if n > 60 {
		return 60
	}
	return n
}
func IsAccountRPMError(err error) bool {
	var local *AccountRPMError
	return errors.As(err, &local)
}

func accountRPMWSTurnError(err error, turn int) error {
	var local *AccountRPMError
	if turn > 1 && errors.As(err, &local) {
		local.NoMigration = true
	}
	return err
}

type accountRPMAccountReader interface {
	GetByID(context.Context, int64) (*Account, error)
}

// Optional narrow query avoids hydrating credentials/groups on every send.
type AccountRPMLimitReader interface {
	GetAccountRPMLimit(context.Context, int64) (int, error)
}

type accountRPMInternalProbeKey struct{}

// Called only at a dispatch boundary. Redis retries reuse the ID within Run;
// another upstream transmission always calls this function again with a new ID.
func admitAccountRPM(ctx context.Context, cache GatewayCache, repo accountRPMAccountReader, account *Account) error {
	limit, err := accountRPMDispatchLimit(ctx, repo, account)
	if err != nil || limit == 0 {
		return err
	}
	admission, ok := cache.(AccountRPMAdmissionCache)
	if !ok {
		return &AccountRPMError{AccountID: account.ID, Limit: limit, Unavailable: true}
	}
	admitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	decision, err := admission.AdmitAccountRPM(admitCtx, account.ID, limit, uuid.NewString())
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &AccountRPMError{AccountID: account.ID, Limit: limit, Unavailable: true, Cause: err}
	}
	if !decision.Allowed {
		return &AccountRPMError{AccountID: account.ID, Limit: limit, Decision: decision}
	}
	return ctx.Err()
}

func accountRPMDispatchLimit(ctx context.Context, repo accountRPMAccountReader, account *Account) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if exempt, _ := ctx.Value(accountRPMInternalProbeKey{}).(bool); exempt {
		return 0, nil
	}
	if account == nil {
		return 0, &AccountRPMError{Unavailable: true}
	}
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	limit, err := freshAccountRPMLimit(readCtx, repo, account)
	if err == nil && (limit < 0 || limit > AccountRPMLimitMax) {
		err = invalidAccountRPMLimit()
	}
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		// A stale unlimited snapshot may survive a storage outage, but must not
		// override authoritative evidence of invalid configuration or deletion.
		if !errors.Is(err, invalidAccountRPMLimit()) && !errors.Is(err, ErrAccountNotFound) {
			if selectedLimit, selectedErr := account.AccountRPMLimit(); selectedErr == nil && selectedLimit == 0 {
				return 0, nil
			}
		}
		return 0, &AccountRPMError{AccountID: account.ID, Unavailable: true, Cause: err}
	}
	return limit, nil
}

func freshAccountRPMLimit(ctx context.Context, repo accountRPMAccountReader, account *Account) (int, error) {
	if reader, ok := repo.(AccountRPMLimitReader); ok {
		return reader.GetAccountRPMLimit(ctx, account.ID)
	}
	if repo != nil {
		latest, err := repo.GetByID(ctx, account.ID)
		if err != nil {
			return 0, err
		}
		if latest == nil {
			return 0, ErrAccountNotFound
		}
		return latest.AccountRPMLimit()
	}
	return account.AccountRPMLimit()
}

func AccountRPMCapacities(ctx context.Context, cache GatewayCache, accounts []Account) map[int64]AccountRPMCapacity {
	out := make(map[int64]AccountRPMCapacity, len(accounts))
	limits := make(map[int64]int)
	for i := range accounts {
		a := &accounts[i]
		limit, err := a.AccountRPMLimit()
		if err == nil && limit == 0 {
			out[a.ID] = AccountRPMCapacity{Status: "unlimited"}
		} else {
			out[a.ID] = AccountRPMCapacity{Status: "unavailable"}
			if err == nil {
				limits[a.ID] = limit
			}
		}
	}
	reader, ok := cache.(AccountRPMAdmissionCache)
	if !ok || len(limits) == 0 {
		return out
	}
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	counts, err := reader.ReadAccountRPMBatch(readCtx, limits)
	if err != nil {
		return out
	}
	for id, limit := range limits {
		count, ok := counts[id]
		if !ok {
			continue
		}
		used, remaining := count.Used, max(0, limit-count.Used)
		state := AccountRPMCapacity{Status: "available", Used: &used, Remaining: &remaining}
		if used >= limit {
			state.Status = "saturated"
		}
		if !count.ResetAt.IsZero() {
			reset := count.ResetAt
			state.ResetAt = &reset
		}
		out[id] = state
	}
	return out
}

func (s *OpenAIGatewayService) AccountRPMCapacities(ctx context.Context, accounts []Account) map[int64]AccountRPMCapacity {
	var cache GatewayCache
	if s != nil {
		cache = s.cache
	}
	return AccountRPMCapacities(ctx, cache, accounts)
}
