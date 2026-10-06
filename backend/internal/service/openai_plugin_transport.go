package service

import "net/http"

func (s *OpenAIGatewayService) SetPluginManager(manager *PluginManager) {
	s.pluginManager = manager
}

// doOpenAIUpstream 只在 OpenAI OAuth 能力绑定已启用时把真实请求交给插件。
// 插件返回标准 http.Response，响应解析、错误映射、SSE 和计费仍由现有核心链处理。
func (s *OpenAIGatewayService) doOpenAIUpstream(request *http.Request, proxyURL string, account *Account) (*http.Response, error) {
	var err error
	request, err = accountRPMDispatchRequest(request, s.cache, s.accountRepo, account)
	if err != nil {
		return nil, err
	}
	return s.doOpenAIUpstreamWithAdmission(request, proxyURL, account)
}

func (s *OpenAIGatewayService) doOpenAIUpstreamWithAdmission(request *http.Request, proxyURL string, account *Account) (*http.Response, error) {
	if s.pluginManager != nil {
		response, handled, err := s.pluginManager.RoundTripOpenAIOAuth(request.Context(), request, proxyURL, account)
		if handled {
			return response, err
		}
	}
	return accountRPMSendHTTP(s.httpUpstream, account, request, proxyURL, nil)
}

// doOpenAIAccountTestUpstream 让 OpenAI OAuth 账号测试与真实转发使用同一插件路径。
// API Key 和未命中插件的账号保持各自原有的 HTTPUpstream 行为。
func (s *AccountTestService) doOpenAIAccountTestUpstream(
	request *http.Request,
	proxyURL string,
	account *Account,
	useTLSFallback bool,
) (*http.Response, error) {
	if err := s.checkOpenAIEvalAutomaticAccount(request.Context(), account); err != nil {
		if request.Body != nil {
			_ = request.Body.Close()
		}
		return nil, err
	}
	if automatic, _ := request.Context().Value(openAIEvalAutomaticKey{}).(bool); automatic {
		// PluginManager rejects only a selected plugin without the single-send
		// contract, leaving unbound accounts on the guarded HTTP fallback.
		request = request.WithContext(WithHTTPUpstreamSingleSend(request.Context()))
	}
	if s.pluginManager != nil {
		response, handled, err := s.pluginManager.RoundTripOpenAIOAuth(request.Context(), request, proxyURL, account)
		if handled {
			return response, err
		}
	}
	if useTLSFallback {
		return s.doAccountTestUpstreamTLS(
			request,
			proxyURL,
			account,
			s.tlsFPProfileService.ResolveTLSProfile(account),
		)
	}
	return s.doAccountTestUpstream(request, proxyURL, account, nil)
}
