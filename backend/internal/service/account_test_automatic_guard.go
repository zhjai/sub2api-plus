package service

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

// Manual diagnostics keep their original transport. Scheduled diagnostics
// recheck administrator participation at each send, not just task pickup.
func (s *AccountTestService) doAccountTestUpstream(req *http.Request, proxy string, account *Account, profile *tlsfingerprint.Profile) (*http.Response, error) {
	return s.doAccountTestUpstreamMode(req, proxy, account, profile, false)
}

func (s *AccountTestService) doAccountTestUpstreamTLS(req *http.Request, proxy string, account *Account, profile *tlsfingerprint.Profile) (*http.Response, error) {
	return s.doAccountTestUpstreamMode(req, proxy, account, profile, true)
}

func (s *AccountTestService) doAccountTestUpstreamMode(req *http.Request, proxy string, account *Account, profile *tlsfingerprint.Profile, tlsFallback bool) (*http.Response, error) {
	if err := s.checkOpenAIEvalAutomaticAccount(req.Context(), account); err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	if automatic, _ := req.Context().Value(openAIEvalAutomaticKey{}).(bool); !automatic {
		if tlsFallback {
			return s.httpUpstream.DoWithTLS(req, proxy, account.ID, account.Concurrency, profile)
		}
		return s.httpUpstream.Do(req, proxy, account.ID, account.Concurrency)
	}
	req = req.WithContext(WithHTTPUpstreamSingleSend(req.Context()))
	if transport, ok := s.httpUpstream.(HTTPUpstreamSingleSend); !ok || !transport.SupportsSingleSend() {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, &HTTPUpstreamSingleSendUnsupportedError{}
	}
	var cache GatewayCache
	if s.openaiGatewayService != nil {
		cache = s.openaiGatewayService.cache
	}
	req, err := accountRPMDispatchRequest(req, cache, s.accountRepo, account)
	if err != nil {
		return nil, err
	}
	if transport, ok := s.httpUpstream.(interface{ HandlesAccountRPMAdmission() bool }); !ok || !transport.HandlesAccountRPMAdmission() {
		if err := AdmitAccountRPMHTTPRetry(req.Context()); err != nil {
			if req.Body != nil {
				_ = req.Body.Close()
			}
			return nil, err
		}
	}
	if tlsFallback {
		return s.httpUpstream.DoWithTLS(req, proxy, account.ID, account.Concurrency, profile)
	}
	return s.httpUpstream.Do(req, proxy, account.ID, account.Concurrency)
}
