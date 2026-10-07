package service

import (
	"context"
	"errors"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

type accountRPMRetryAdmissionKey struct{}
type accountRPMHTTPPolicy struct {
	limited func(context.Context) (bool, error)
	admit   func(context.Context) error
}

// Carry a fresh admission callback to each actual transport dispatch, including
// redirects and the Grok fallback below Do. This never reserves a permit.
func accountRPMDispatchRequest(req *http.Request, cache GatewayCache, repo accountRPMAccountReader, account *Account) (*http.Request, error) {
	req = withCodexOutboundDiagnostics(req, account)
	if err := req.Context().Err(); err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	admit := func(ctx context.Context) error {
		if automatic, _ := ctx.Value(openAIEvalAutomaticKey{}).(bool); automatic {
			if repo == nil || account == nil {
				return &OpenAIEvalRequestError{Code: "account_lookup_unavailable", Message: "automatic test account lookup is unavailable"}
			}
			latest, err := repo.GetByID(ctx, account.ID)
			if err != nil || latest == nil {
				return &OpenAIEvalRequestError{Code: "account_lookup_unavailable", Message: "automatic test account lookup failed"}
			}
			if !latest.Schedulable {
				return &OpenAIEvalRequestError{Code: "account_scheduling_disabled", Message: "automatic test skipped: account scheduling is disabled"}
			}
		}
		if err := admitAccountRPM(ctx, cache, repo, account); err != nil {
			return err
		}
		return openAIEvalBeforeSend(ctx)
	}
	policy := accountRPMHTTPPolicy{
		admit: admit,
		limited: func(ctx context.Context) (bool, error) {
			limit, err := accountRPMDispatchLimit(ctx, repo, account)
			return limit > 0, err
		},
	}
	return req.WithContext(context.WithValue(req.Context(), accountRPMRetryAdmissionKey{}, policy)), nil
}

// WithAccountRPMHTTPAdmission attaches an internal callback, not a client ID or
// an admission receipt. Each transport send invokes it independently.
func WithAccountRPMHTTPAdmission(ctx context.Context, admit func(context.Context) error) context.Context {
	return context.WithValue(ctx, accountRPMRetryAdmissionKey{}, accountRPMHTTPPolicy{
		admit:   admit,
		limited: func(context.Context) (bool, error) { return true, nil },
	})
}

func AdmitAccountRPMHTTPRetry(ctx context.Context) error {
	if policy, ok := ctx.Value(accountRPMRetryAdmissionKey{}).(accountRPMHTTPPolicy); ok && policy.admit != nil {
		return policy.admit(ctx)
	}
	return nil
}

// AccountRPMHTTPIsLimited selects the non-replaying transport without consuming
// capacity. Admission refreshes this configuration again immediately before send.
func AccountRPMHTTPIsLimited(ctx context.Context) (bool, error) {
	if policy, ok := ctx.Value(accountRPMRetryAdmissionKey{}).(accountRPMHTTPPolicy); ok && policy.limited != nil {
		return policy.limited(ctx)
	}
	return false, nil
}

func accountRPMPluginDispatchCheck(ctx context.Context, account *Account) error {
	limited, err := AccountRPMHTTPIsLimited(ctx)
	if err != nil {
		return err
	}
	if limited {
		return &AccountRPMError{AccountID: account.ID, Unavailable: true, Cause: errors.New("plugin transport does not support per-send RPM admission")}
	}
	return nil
}

func accountRPMDo(upstream HTTPUpstream, cache GatewayCache, repo accountRPMAccountReader, account *Account, req *http.Request, proxy string, profile *tlsfingerprint.Profile) (*http.Response, error) {
	req, err := accountRPMDispatchRequest(req, cache, repo, account)
	if err != nil {
		return nil, err
	}
	return accountRPMSendHTTP(upstream, account, req, proxy, profile)
}

func accountRPMSendHTTP(upstream HTTPUpstream, account *Account, req *http.Request, proxy string, profile *tlsfingerprint.Profile) (*http.Response, error) {
	// Production implements this optional capability at RoundTrip, after host
	// and proxy validation. Existing mock/custom upstreams admit at their Do.
	transport, ok := upstream.(interface{ HandlesAccountRPMAdmission() bool })
	if !ok || !transport.HandlesAccountRPMAdmission() {
		if err := AdmitAccountRPMHTTPRetry(req.Context()); err != nil {
			if req.Body != nil {
				_ = req.Body.Close()
			}
			return nil, err
		}
	}
	if profile != nil {
		return upstream.DoWithTLS(req, proxy, account.ID, account.Concurrency, profile)
	}
	return upstream.Do(req, proxy, account.ID, account.Concurrency)
}

func (s *GatewayService) doAccountRPMUpstream(req *http.Request, proxy string, account *Account) (*http.Response, error) {
	return accountRPMDo(s.httpUpstream, s.cache, s.accountRepo, account, req, proxy, nil)
}
func (s *GatewayService) doAccountRPMUpstreamTLS(req *http.Request, proxy string, account *Account, profile *tlsfingerprint.Profile) (*http.Response, error) {
	return accountRPMDo(s.httpUpstream, s.cache, s.accountRepo, account, req, proxy, profile)
}
func (s *OpenAIGatewayService) doAccountRPMUpstream(req *http.Request, proxy string, account *Account) (*http.Response, error) {
	return accountRPMDo(s.httpUpstream, s.cache, s.accountRepo, account, req, proxy, nil)
}
func (s *GeminiMessagesCompatService) doAccountRPMUpstream(req *http.Request, proxy string, account *Account) (*http.Response, error) {
	return accountRPMDo(s.httpUpstream, s.cache, s.accountRepo, account, req, proxy, nil)
}
func (s *AntigravityGatewayService) doAccountRPMUpstream(req *http.Request, proxy string, account *Account) (*http.Response, error) {
	return accountRPMDo(s.httpUpstream, s.cache, s.accountRepo, account, req, proxy, nil)
}

func (s *OpenAIGatewayService) admitAccountRPM(ctx context.Context, account *Account) error {
	return admitAccountRPM(ctx, s.cache, s.accountRepo, account)
}
