package service

import (
	"context"
	"errors"
	"time"
)

type openAIEvalAutomaticKey struct{}

func (s *AccountTestService) checkOpenAIEvalAutomaticAccount(ctx context.Context, account *Account) error {
	if ctx.Err() != nil {
		failure := openAIEvalIOError(ctx, ctx.Err(), 0)
		failure.Attempted = false
		return failure
	}
	if automatic, _ := ctx.Value(openAIEvalAutomaticKey{}).(bool); !automatic {
		return nil
	}
	if s.accountRepo == nil || account == nil {
		return &OpenAIEvalRequestError{Code: "account_lookup_unavailable", Message: "automatic evaluation account lookup is unavailable"}
	}
	latest, err := s.accountRepo.GetByID(ctx, account.ID)
	if err != nil {
		return &OpenAIEvalRequestError{Code: "account_lookup_unavailable", Message: "automatic evaluation account lookup failed"}
	}
	if latest == nil || !latest.IsActive() || !latest.Schedulable {
		return &OpenAIEvalRequestError{Code: "account_scheduling_disabled", Message: "automatic evaluation skipped: account scheduling is disabled"}
	}
	return ctx.Err()
}

func (s *AccountTestService) acquireOpenAIEvalAccountSlot(ctx context.Context, account *Account) (func(), error) {
	if err := s.checkOpenAIEvalAutomaticAccount(ctx, account); err != nil {
		return nil, err
	}
	if s.openaiGatewayService == nil || s.openaiGatewayService.concurrencyService == nil {
		return func() {}, nil
	}
	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	backoff := 100 * time.Millisecond
	for {
		if waitCtx.Err() != nil {
			if ctx.Err() != nil {
				return nil, &OpenAIEvalRequestError{Code: "cancelled", Message: "evaluation concurrency wait cancelled"}
			}
			return nil, &OpenAIEvalRequestError{Code: "concurrency_wait_timeout", Message: "evaluation concurrency wait expired"}
		}
		if err := s.checkOpenAIEvalAutomaticAccount(waitCtx, account); err != nil {
			return nil, err
		}
		result, err := s.openaiGatewayService.tryAcquireAccountSlot(waitCtx, account.ID, account.Concurrency)
		if err != nil {
			return nil, &OpenAIEvalRequestError{Code: "concurrency_unavailable", Message: "failed to reserve evaluation account concurrency: " + err.Error()}
		}
		if result.Acquired {
			if waitCtx.Err() != nil {
				result.ReleaseFunc()
				continue
			}
			return result.ReleaseFunc, nil
		}
		// Share the normal gateway slots; waiting must not consume a request attempt.
		timer := time.NewTimer(backoff)
		select {
		case <-waitCtx.Done():
			timer.Stop()
		case <-timer.C:
		}
		backoff = min(backoff*2, 2*time.Second)
	}
}

// A denied local permit made no upstream transmission. The single-send call
// has already released its concurrency slot before this bounded wait begins.
func waitOpenAIEvalAdmission(ctx context.Context, deadline time.Time, err error) (bool, error) {
	var limited *AccountRPMError
	if !errors.As(err, &limited) {
		return false, nil
	}
	if ctx.Err() != nil {
		return false, &OpenAIEvalRequestError{Code: "cancelled", Message: "evaluation admission cancelled"}
	}
	if limited.Unavailable {
		return false, &OpenAIEvalRequestError{Code: "account_rpm_unavailable", Message: "evaluation RPM admission unavailable"}
	}
	if !time.Now().Before(deadline) {
		return false, &OpenAIEvalRequestError{Code: "account_rpm_wait_timeout", Message: "evaluation RPM wait expired"}
	}
	delay := limited.Decision.RetryAfter
	if delay < 100*time.Millisecond {
		delay = 100 * time.Millisecond
	}
	if delay > time.Second {
		delay = time.Second
	}
	remaining := time.Until(deadline)
	if delay > remaining {
		delay = remaining
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false, &OpenAIEvalRequestError{Code: "cancelled", Message: "evaluation admission cancelled"}
	case <-timer.C:
		return true, nil
	}
}
