package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// LatestModelQualityRuns derives scheduling evidence without rewriting runs or
// using detector aliases. Invalid/failed attempts cannot hide a usable result
// at another effort (or an earlier usable result at the same effort).
func (r *openAIEvalRepository) LatestModelQualityRuns(ctx context.Context, keys []service.OpenAIEvalEvidenceKey, now time.Time) ([]service.OpenAIEvalRun, error) {
	if len(keys) > 600 {
		return nil, errors.New("model evidence batch exceeds 600 routes")
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
 JOIN openai_eval_runs r ON r.account_id=k.account_id
   AND lower(btrim(r.requested_model))=lower(btrim(k.requested_model)) AND r.test_type=k.test_type
 WHERE r.trigger_source IN ('manual','scheduled') AND r.finished_at <= $2
   AND r.status IN ('pass','warning','suspected_normal','suspected_warning')
   AND r.finished_at > $4 AND r.data_version=$3 AND NOT (r.outcome @> '{"diagnostic_only": true}'::jsonb)
 ORDER BY r.finished_at DESC,r.id DESC`, payload, now, service.OpenAIEvalQualityDataVersion, now.Add(-service.OpenAIEvalQualityMaxTTL))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	selected := make(map[service.OpenAIEvalEvidenceKey]bool)
	result := make([]service.OpenAIEvalRun, 0, len(keys))
	for rows.Next() {
		var run service.OpenAIEvalRun
		var outcome, samples []byte
		if err := rows.Scan(&run.ID, &run.AccountID, &run.TestType, &run.RequestedModel, &run.ReasoningEffort, &run.DataVersion, &run.BaselineVersion, &run.Status, &outcome, &samples, &run.FinishedAt, &run.TriggerSource, &run.Error); err != nil {
			return nil, err
		}
		key := service.OpenAIEvalEvidenceKey{AccountID: run.AccountID, RequestedModel: strings.ToLower(strings.TrimSpace(run.RequestedModel)), TestType: run.TestType}
		if selected[key] {
			continue
		}
		if unmarshalOpenAIEvalRunOutcome(outcome, &run) != nil || json.Unmarshal(samples, &run.Samples) != nil || !service.OpenAIEvalUsableModelQualityRun(run, now) {
			continue
		}
		selected[key] = true
		result = append(result, run)
		if len(selected) == len(keys) {
			break
		}
	}
	return result, rows.Err()
}

var _ service.OpenAIEvalModelEvidenceRepository = (*openAIEvalRepository)(nil)
