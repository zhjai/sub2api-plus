package repository

import (
	"context"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbaccount "github.com/Wei-Shaw/sub2api/ent/account"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// ValidateCodexIdentityBinding deliberately bypasses scheduler snapshots. This
// rechecks key revocation and duplicate credentials on every experimental send.
func (r *accountRepository) ValidateCodexIdentityBinding(ctx context.Context, a *service.Account, keyID, userID int64) error {
	return validateCodexIdentityBinding(ctx, clientFromContext(ctx, r.client), a, keyID, userID)
}

func validateCodexIdentityBinding(ctx context.Context, client *dbent.Client, a *service.Account, keyID, userID int64) error {
	if keyID > 0 {
		key, err := client.APIKey.Get(ctx, keyID)
		if err != nil || key == nil || key.DeletedAt != nil || key.Status != service.StatusActive || (key.ExpiresAt != nil && !key.ExpiresAt.After(time.Now())) || (key.Quota > 0 && key.QuotaUsed >= key.Quota) {
			return infraerrors.BadRequest("CODEX_IDENTITY_API_KEY_UNAVAILABLE", "request API key must exist, be active, unexpired and have available quota")
		}
		if userID > 0 && key.UserID != userID {
			return infraerrors.BadRequest("CODEX_IDENTITY_PRINCIPAL_CHANGED", "request API key principal changed; reauthenticate and start a new session")
		}
		owner, err := client.User.Get(ctx, key.UserID)
		if err != nil || owner == nil || owner.DeletedAt != nil || owner.Status != service.StatusActive {
			return infraerrors.BadRequest("CODEX_IDENTITY_API_KEY_UNAVAILABLE", "request API key owner is unavailable")
		}
	}
	namespace := service.CodexIdentityNamespace(a)
	if namespace == "" {
		return infraerrors.BadRequest("CODEX_IDENTITY_NAMESPACE_REQUIRED", "stable credential namespace is required")
	}
	// Include inactive and unschedulable rows: they can be re-enabled, and must
	// never create a second authorization for the same actual credential.
	rows, err := client.Account.Query().Where(dbaccount.PlatformEQ(service.PlatformOpenAI), dbaccount.DeletedAtIsNil()).All(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.ID == a.ID || row.Extra[service.CodexIdentityModeKey] != service.CodexIdentityPreserveClient {
			continue
		}
		other := accountEntityToService(row)
		// A stale stored namespace still reserves its authorization until edited.
		if service.CodexIdentityNamespace(other) == namespace || other.Extra[service.CodexIdentityNamespaceKey] == namespace {
			return infraerrors.BadRequest("CODEX_IDENTITY_NAMESPACE_CONFLICT", "actual credential namespace already has an identity experiment binding; disable the other binding first")
		}
	}
	return nil
}

func normalizeAccountCodexIdentity(ctx context.Context, client *dbent.Client, a *service.Account) error {
	managed := false
	for key := range a.Extra {
		if strings.HasPrefix(key, "codex_identity_") {
			managed = true
			break
		}
	}
	if !managed {
		return nil
	}
	var previous *service.Account
	if a.ID > 0 {
		row, err := client.Account.Get(ctx, a.ID)
		if err != nil {
			return err
		}
		previous = accountEntityToService(row)
	}
	if err := service.NormalizeCodexIdentityConfig(a, previous); err != nil {
		return err
	}
	if a.Extra[service.CodexIdentityModeKey] == service.CodexIdentityPreserveClient {
		if previous != nil && previous.Extra[service.CodexIdentityModeKey] == service.CodexIdentityPreserveClient &&
			previous.Extra[service.CodexIdentityRevisionKey] == a.Extra[service.CodexIdentityRevisionKey] {
			// Identity is account-scoped; live principal checks happen per request.
			return nil
		}
		return validateCodexIdentityBinding(ctx, client, a, 0, 0)
	}
	return nil
}
