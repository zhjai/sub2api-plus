package service

import (
	"context"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// Empty mappings must not inherit the native OAuth model fallback. A concrete
// group selection can supply the BPS catalog without an account-level mapping.
// Use the authenticated group already in context; this gate performs no I/O.
func excelBPSModelConfigurationEligible(ctx context.Context, account *Account, requestedModel string) bool {
	if account == nil || !account.IsExcelOAuth() || len(account.GetModelMapping()) > 0 {
		return true
	}
	group, _ := ctx.Value(ctxkey.Group).(*Group)
	if group == nil || !group.ModelAllowlistEnabled() {
		return false
	}
	model := requestedModel
	if public, ok := RequestedPublicModelFromContext(ctx); ok && strings.TrimSpace(public) != "" {
		model = public
	} else if client := OpenAIClientRequestedModelFromContext(ctx); strings.TrimSpace(client) != "" {
		model = client
	}
	for _, entry := range group.ModelAllowlist.Models {
		entry = strings.TrimSpace(entry)
		if entry != "" && !strings.Contains(entry, "*") && (GroupModelAllowlist{Enabled: true, Models: []string{entry}}).Allows(model) {
			return true
		}
	}
	return false
}
