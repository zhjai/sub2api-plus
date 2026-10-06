package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/prism"
)

func resolvePrismEvalTarget(ctx context.Context, account *Account, requested string) (*OpenAIEvalTarget, error) {
	if strings.TrimSpace(requested) == "" {
		return nil, errors.New("Prism evaluation model is required")
	}
	model, _, err := ResolvePrismAccountModel(ctx, account, requested, "")
	if err != nil {
		return nil, err
	}
	return &OpenAIEvalTarget{Account: account, Credential: account, RequestedModel: requested, UpstreamModel: model}, nil
}

// validatePrismEvalRoute validates dynamic account capabilities independently of
// the OpenAI static model list. Baseline coverage is checked separately.
func (s *OpenAIEvalService) validatePrismEvalRoute(ctx context.Context, id int64, model, effort string) (bool, error) {
	if s.accountTest != nil && s.accountTest.openaiGatewayService != nil {
		ctx = withPrismLifecycle(ctx, s.accountTest.openaiGatewayService.prismAccountService)
	}
	if s.accounts == nil {
		return false, nil
	}
	account, err := s.accounts.GetByID(ctx, id)
	if err != nil {
		return false, err
	}
	if account == nil || account.Platform != "prism" {
		return false, nil
	}
	_, _, err = ResolvePrismAccountModel(ctx, account, model, effort)
	return true, err
}

func (s *AccountTestService) runPrismEvalSample(ctx context.Context, target *OpenAIEvalTarget, prompt, effort string) (*OpenAIEvalSampleResponse, error) {
	if s.openaiGatewayService != nil && s.openaiGatewayService.prismAccountService != nil {
		fresh, err := s.openaiGatewayService.prismAccountService.EnsureFresh(ctx, target.Account)
		if err != nil {
			return nil, err
		}
		// A sample always uses its freshly persisted credential generation.
		current := *target
		current.Account, current.Credential = fresh, fresh
		target = &current
	}
	// Validate before reserving admission. Catalog failures consume no attempt.
	model, _, err := ResolvePrismAccountModel(ctx, target.Account, target.RequestedModel, effort)
	if err != nil {
		return nil, &OpenAIEvalRequestError{Code: "unsupported_route", Message: safePrismError(err).Error()}
	}
	if model != target.UpstreamModel {
		return nil, &OpenAIEvalRequestError{Code: "route_changed", Message: "Prism evaluation model mapping changed during the run"}
	}
	release, err := s.acquireOpenAIEvalAccountSlot(ctx, target.Account)
	if err != nil {
		return nil, err
	}
	defer release()
	requestCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := s.checkOpenAIEvalAutomaticAccount(requestCtx, target.Account); err != nil {
		return nil, err
	}
	var cache GatewayCache
	if s.openaiGatewayService != nil {
		cache = s.openaiGatewayService.cache
	}
	if err := admitAccountRPM(requestCtx, cache, s.accountRepo, target.Account); err != nil {
		return nil, err
	}
	status, err := RunPrismText(requestCtx, target.Account, target.RequestedModel, effort, prompt)
	if err != nil {
		// Native execution must never transparently replay a generation. A
		// caller can explicitly start a new evaluation sample after a failure.
		return nil, &OpenAIEvalRequestError{Code: "prism_upstream_error", Message: safePrismError(err).Error(), Attempted: true}
	}
	return prismEvalSampleResponse(status, model)
}

func prismEvalSampleResponse(status *prism.StatusResponse, model string) (*OpenAIEvalSampleResponse, error) {
	if status == nil || !status.Done || status.Fail || status.Error != "" || strings.TrimSpace(status.Text) == "" {
		return nil, &OpenAIEvalRequestError{Code: "prism_incomplete_response", Message: "Prism did not return a successful completed text response", Attempted: true}
	}
	result := &OpenAIEvalSampleResponse{Text: status.Text, Model: model, HTTPStatus: http.StatusOK, CompletedAt: time.Now().UTC()}
	if status.Usage != nil {
		result.InputTokens, result.OutputTokens = int64(status.Usage.InputTokens), int64(status.Usage.OutputTokens)
	}
	return result, nil
}

func prismEvalBaselineSupported(testType, model string) bool {
	switch testType {
	case OpenAIEvalTypeCandy:
		return true
	case OpenAIEvalTypeFingerprint:
		for _, baseline := range OpenAIEvalFingerprintBaselines {
			if baseline.Model == model {
				return true
			}
		}
	case OpenAIEvalTypeModelTrace:
		for _, baseline := range modelTraceBank.Models {
			if baseline.ID == model {
				return true
			}
		}
	}
	return false
}

// Public aliases remain the scheduling dimension, but baseline attribution must
// compare the actual mapped Prism model validated for this run.
func openAIEvalBaselineModel(target *OpenAIEvalTarget, requested string) string {
	if target != nil && target.Account != nil && target.Account.Platform == PlatformPrism {
		return target.UpstreamModel
	}
	return requested
}
