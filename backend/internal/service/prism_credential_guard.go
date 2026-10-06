package service

import (
	"context"
	"reflect"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type verifiedPrismCredentialWriteKey struct{}

// Only the verified lifecycle may replace authentication or its identity marker.
// Ordinary account edits may still modify explicit model aliases.
func withVerifiedPrismCredentialWrite(ctx context.Context) context.Context {
	return context.WithValue(ctx, verifiedPrismCredentialWriteKey{}, true)
}

func validatePrismCredentialWrite(ctx context.Context, credentials map[string]any) error {
	if authorized, _ := ctx.Value(verifiedPrismCredentialWriteKey{}).(bool); authorized {
		return nil
	}
	for key := range credentials {
		if key != "model_mapping" {
			return infraerrors.BadRequest("PRISM_VERIFIED_IMPORT_REQUIRED", "Prism credentials must be imported or refreshed through Prism account verification")
		}
	}
	return nil
}

func validatePrismCredentialUpdate(ctx context.Context, existing, incoming map[string]any) error {
	changes := make(map[string]any)
	for key, value := range incoming {
		// Standard account editors round-trip nonsecret identity metadata. Keeping
		// the stored value is safe; changing or introducing it requires verification.
		if previous, present := existing[key]; present && reflect.DeepEqual(previous, value) {
			continue
		}
		changes[key] = value
	}
	return validatePrismCredentialWrite(ctx, changes)
}
