package service

import (
	"errors"
	"strings"

	"github.com/gin-gonic/gin"
)

func (s *AccountTestService) testPrismAccountConnection(c *gin.Context, account *Account, model, prompt, mode string, opts AccountTestOptions) error {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	fail := func(code, message string) error {
		s.sendEvent(c, TestEvent{Type: "error", Code: code, Error: message})
		return errors.New(message)
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != "" && mode != "default" && mode != "text" || opts.ImageDataURL != "" || opts.AudioDataURL != "" {
		return fail("unsupported_mode", "Prism connectivity testing supports text only; compact and media are unsupported")
	}
	ctx := c.Request.Context()
	if s.openaiGatewayService != nil {
		ctx = withPrismLifecycle(ctx, s.openaiGatewayService.prismAccountService)
	}
	if err := ctx.Err(); err != nil {
		return fail("canceled", "Prism connectivity test canceled")
	}
	model = strings.TrimSpace(model)
	if model == "" {
		catalog, err := PrismAccountCatalog(ctx, account)
		if err != nil {
			return fail("catalog_unavailable", safePrismError(err).Error())
		}
		if len(catalog) == 0 {
			return fail("no_entitlement", "Prism account has no available models")
		}
		model = catalog[0].ID
	}
	target, err := resolvePrismEvalTarget(ctx, account, model)
	if err != nil {
		return fail("unsupported_model", safePrismError(err).Error())
	}
	if strings.TrimSpace(prompt) == "" {
		prompt = "Reply briefly to confirm that you can respond."
	}
	s.sendEvent(c, TestEvent{Type: "test_start", Model: model})
	// The existing evaluation path owns concurrency, RPM, cancellation and
	// completed-text validation; this path introduces no second admission.
	result, err := s.RunOpenAIEvalSample(ctx, target, prompt, "")
	if err != nil {
		var requestErr *OpenAIEvalRequestError
		if errors.As(err, &requestErr) {
			return fail(requestErr.Code, requestErr.Message)
		}
		return fail("prism_upstream_error", safePrismError(err).Error())
	}
	if ctx.Err() != nil {
		return fail("canceled", "Prism connectivity test canceled")
	}
	s.sendEvent(c, TestEvent{Type: "content", Text: result.Text, Model: result.Model})
	s.sendEvent(c, TestEvent{Type: "test_complete", Success: true, Model: model})
	return nil
}
