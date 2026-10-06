package prism

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/httpc"
)

// NewDefault uses the source protocol's calibrated field names and transport
// defaults. The caller owns the HTTP client and its account-specific proxy.
func NewDefault(base *httpc.Client) *Client {
	return NewConfigured(base, config.Default().Upstream)
}

// NewConfigured preserves the caller's upstream headers and retry configuration.
func NewConfigured(base *httpc.Client, up config.UpstreamConfig) *Client {
	return New(base, UpstreamOptions{
		UserAgent: up.UserAgent, Origin: up.Origin, Referer: up.Referer,
		Headers: up.Headers, MaxRetries: up.MaxRetries,
		RetryBackoff: up.RetryBackoff, RetryMaxDelay: up.RetryMaxDelay,
	}, DefaultSchema())
}

// DefaultSchema returns the upstream-calibrated protocol mapping.
func DefaultSchema() SchemaOptions {
	s := config.Default().Facade.Schema
	return SchemaOptions{
		StartPath:            s.StartPath,
		StatusPath:           s.StatusPath,
		StopPath:             s.StopPath,
		FieldModel:           s.FieldModel,
		FieldMessages:        s.FieldMessages,
		FieldInstructions:    s.FieldInstructions,
		FieldInput:           s.FieldInput,
		FieldTools:           s.FieldTools,
		FieldStream:          s.FieldStream,
		FieldPreviousRespID:  s.FieldPreviousRespID,
		FieldUserID:          s.FieldUserID,
		FieldMetadata:        s.FieldMetadata,
		FieldSessionID:       s.FieldSessionID,
		FieldProjectID:       s.FieldProjectID,
		FieldSandboxID:       s.FieldSandboxID,
		FieldConversationID:  s.FieldConversationID,
		FieldRequestID:       s.FieldRequestID,
		FieldTurnState:       s.FieldTurnState,
		FieldResponseID:      s.FieldResponseID,
		FieldReasoning:       s.FieldReasoning,
		FieldReasoningEffort: s.FieldReasoningEffort,
		FieldExtra:           s.FieldExtra,
		RespIDKeys:           s.RespIDKeys,
		RespStatusKeys:       s.RespStatusKeys,
		RespTextKeys:         s.RespTextKeys,
		RespDeltaKeys:        s.RespDeltaKeys,
		RespMessagesKey:      s.RespMessagesKey,
		RespErrorKeys:        s.RespErrorKeys,
		StatusDone:           s.StatusDone,
		StatusFail:           s.StatusFail,
		StatusRun:            s.StatusRun,
	}
}
