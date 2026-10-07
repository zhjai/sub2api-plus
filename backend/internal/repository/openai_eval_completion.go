package repository

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *openAIEvalRepository) ClaimDueSchedulesForCompletion(ctx context.Context, now time.Time, limit int) ([]service.OpenAIEvalScheduledRun, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	owner := hex.EncodeToString(nonce[:])
	// Rotate test types within a batch; oldest due targets within each type
	// precede newly arrived work. Claim locking preserves each target independently.
	rows, err := r.db.QueryContext(ctx, `WITH ranked AS (
 SELECT account_id,test_type,requested_model,reasoning_effort,
 row_number() OVER (PARTITION BY test_type ORDER BY next_run_at,account_id,requested_model,reasoning_effort) AS turn
 FROM openai_eval_schedule_state
 WHERE enabled=TRUE AND next_run_at <= $1 AND reasoning_effort <> '__bps_account__'
 AND (claimed_until IS NULL OR claimed_until <= NOW())
 ), due AS (
 SELECT s.account_id,s.test_type,s.requested_model,s.reasoning_effort,s.next_run_at,r.turn
 FROM openai_eval_schedule_state s JOIN ranked r USING(account_id,test_type,requested_model,reasoning_effort)
 ORDER BY r.turn,s.next_run_at,s.account_id,s.test_type,s.requested_model,s.reasoning_effort
 LIMIT $2 FOR UPDATE OF s SKIP LOCKED
 ), claimed AS (UPDATE openai_eval_schedule_state s
 SET claim_owner=$3,claimed_until=NOW()+INTERVAL '120 seconds',last_run_at=$1,updated_at=NOW()
 FROM due d
 WHERE s.account_id=d.account_id AND s.test_type=d.test_type AND s.requested_model=d.requested_model AND s.reasoning_effort=d.reasoning_effort
 RETURNING s.account_id,s.test_type,s.requested_model,s.reasoning_effort,s.sample_mode,s.sample_count,d.turn,d.next_run_at AS previous_due)
 SELECT account_id,test_type,requested_model,reasoning_effort,sample_mode,sample_count FROM claimed
 ORDER BY turn,previous_due,account_id,test_type,requested_model,reasoning_effort`, now, limit, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []service.OpenAIEvalScheduledRun{}
	for rows.Next() {
		item := service.OpenAIEvalScheduledRun{ClaimOwner: owner, ClaimedAt: now}
		if err := rows.Scan(&item.AccountID, &item.TestType, &item.RequestedModel, &item.ReasoningEffort, &item.SampleMode, &item.SampleCount); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *openAIEvalRepository) RenewScheduleClaim(ctx context.Context, item service.OpenAIEvalScheduledRun) (bool, error) {
	result, err := r.db.ExecContext(ctx, `UPDATE openai_eval_schedule_state SET claimed_until=NOW()+INTERVAL '120 seconds'
 WHERE account_id=$1 AND test_type=$2 AND requested_model=$3 AND reasoning_effort=$4 AND claim_owner=$5 AND claimed_until>NOW()`, item.AccountID, item.TestType, item.RequestedModel, item.ReasoningEffort, item.ClaimOwner)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (r *openAIEvalRepository) CompleteSchedule(ctx context.Context, item service.OpenAIEvalScheduledRun, now time.Time) error {
	// Read the current saved interval and enabled flag here: concurrent edits
	// retain their values, and a completed/aborted run never catches up old ticks.
	_, err := r.db.ExecContext(ctx, `UPDATE openai_eval_schedule_state
 SET next_run_at=CASE WHEN enabled THEN $6::timestamptz + ((interval_seconds::bigint + floor(random()*(jitter_seconds::bigint+1))) * INTERVAL '1 second') ELSE NULL END,
 claim_owner=NULL,claimed_until=NULL,updated_at=NOW()
 WHERE account_id=$1 AND test_type=$2 AND requested_model=$3 AND reasoning_effort=$4 AND claim_owner=$5`, item.AccountID, item.TestType, item.RequestedModel, item.ReasoningEffort, item.ClaimOwner, now)
	return err
}

var _ service.OpenAIEvalCompletionRepository = (*openAIEvalRepository)(nil)
