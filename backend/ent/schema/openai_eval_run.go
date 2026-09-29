package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// OpenAIEvalRun stores bounded evaluation history and scheduling audit data.
type OpenAIEvalRun struct {
	ent.Schema
}

func (OpenAIEvalRun) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "openai_eval_runs"}}
}

func (OpenAIEvalRun) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("account_id"),
		field.String("test_type").NotEmpty().MaxLen(24),
		field.String("requested_model").NotEmpty().MaxLen(200),
		field.String("upstream_model").Default("").MaxLen(200),
		field.String("reasoning_effort").Default("").MaxLen(24),
		field.String("data_version").NotEmpty().MaxLen(100),
		field.String("baseline_version").Default("").MaxLen(120),
		field.String("status").NotEmpty().MaxLen(24),
		field.JSON("outcome", map[string]any{}).Default(map[string]any{}),
		field.JSON("samples", []map[string]any{}).Default([]map[string]any{}),
		field.Int("request_count").Default(0),
		field.Int64("input_tokens").Default(0),
		field.Int64("output_tokens").Default(0),
		field.Float("cost_estimate_usd").Optional().Nillable(),
		field.Int64("duration_ms").Default(0),
		field.Time("started_at"),
		field.Time("finished_at").Optional().Nillable(),
		field.Int64("triggered_by").Default(0),
		field.String("trigger_source").NotEmpty().MaxLen(24),
		field.String("error_code").Default("").MaxLen(80),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("retention_until").SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (OpenAIEvalRun) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("account_id", "requested_model", "reasoning_effort", "test_type", "created_at"),
		index.Fields("created_at"),
		index.Fields("retention_until"),
	}
}
