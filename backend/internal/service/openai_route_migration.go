package service

import (
	"context"
	"math"
	"strings"
)

type openAIRouteMigrationContextKey struct{}
type openAIClientRequestedModelContextKey struct{}

// WithOpenAIClientRequestedModel stores the public model id sent by the
// client, independently from any account/group-specific upstream mapping.
func WithOpenAIClientRequestedModel(ctx context.Context, model string) context.Context {
	if ctx == nil {
		return nil
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return ctx
	}
	return context.WithValue(ctx, openAIClientRequestedModelContextKey{}, model)
}

// OpenAIClientRequestedModelFromContext returns the public model id, if bound.
func OpenAIClientRequestedModelFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	model, _ := ctx.Value(openAIClientRequestedModelContextKey{}).(string)
	return strings.TrimSpace(model)
}

// OpenAIRouteMigration describes a request-local escape from an account's
// billing-rate channel. It is deliberately carried in context so existing
// scheduler entry points remain source-compatible.
type OpenAIRouteMigration struct {
	Active          bool
	FailedRate      float64
	RequestedModel  string
	RequestedEffort string
}

// WithOpenAIRouteMigration marks subsequent sampling attempts as a model/
// effort-specific rate-ladder migration. A non-finite or negative rate is
// ignored so malformed account metadata cannot reorder the whole pool.
func WithOpenAIRouteMigration(ctx context.Context, failedRate float64, model, effort string) context.Context {
	if ctx == nil || math.IsNaN(failedRate) || math.IsInf(failedRate, 0) || failedRate < 0 {
		return ctx
	}
	return context.WithValue(ctx, openAIRouteMigrationContextKey{}, OpenAIRouteMigration{
		Active:          true,
		FailedRate:      failedRate,
		RequestedModel:  model,
		RequestedEffort: effort,
	})
}

func OpenAIRouteMigrationFromContext(ctx context.Context) (OpenAIRouteMigration, bool) {
	if ctx == nil {
		return OpenAIRouteMigration{}, false
	}
	migration, ok := ctx.Value(openAIRouteMigrationContextKey{}).(OpenAIRouteMigration)
	return migration, ok && migration.Active
}
