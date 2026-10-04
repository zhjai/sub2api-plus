package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *openAIEvalRepository) LatestScheduledRuns(ctx context.Context, keys []service.OpenAIEvalEvidenceKey) ([]service.OpenAIEvalRun, error) {
	if len(keys) > 600 {
		return nil, errors.New("latest evidence batch exceeds 600 routes")
	}
	if len(keys) == 0 {
		return []service.OpenAIEvalRun{}, nil
	}
	payload, err := json.Marshal(keys)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT r.id, r.account_id, r.test_type, r.requested_model, r.reasoning_effort,
		       r.data_version, r.baseline_version, r.status, r.outcome, r.samples,
		       r.finished_at, r.trigger_source, r.error_code
		FROM jsonb_to_recordset($1::jsonb) AS k(account_id bigint, requested_model text, reasoning_effort text, test_type text)
		JOIN LATERAL (
		  SELECT * FROM openai_eval_runs
		  WHERE account_id=k.account_id AND requested_model=k.requested_model
		    AND reasoning_effort=k.reasoning_effort AND test_type=k.test_type
		    AND trigger_source='scheduled' AND finished_at IS NOT NULL AND status <> 'running'
		  ORDER BY finished_at DESC, id DESC LIMIT 1
		) r ON true`, payload)
	if err != nil {
		return nil, fmt.Errorf("load latest scheduled evidence: %w", err)
	}
	defer rows.Close()
	result := make([]service.OpenAIEvalRun, 0, len(keys))
	for rows.Next() {
		var run service.OpenAIEvalRun
		var outcome, samples []byte
		if err := rows.Scan(&run.ID, &run.AccountID, &run.TestType, &run.RequestedModel, &run.ReasoningEffort,
			&run.DataVersion, &run.BaselineVersion, &run.Status, &outcome, &samples, &run.FinishedAt, &run.TriggerSource, &run.Error); err != nil {
			return nil, err
		}
		// Malformed latest diagnostics stay unknown, never uncover an older pass.
		if json.Unmarshal(outcome, &run.Outcome) != nil || json.Unmarshal(samples, &run.Samples) != nil {
			run.Status = "insufficient"
		}
		result = append(result, run)
	}
	return result, rows.Err()
}

var _ service.OpenAIEvalLatestEvidenceRepository = (*openAIEvalRepository)(nil)
