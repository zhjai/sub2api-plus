package repository

import (
	"context"
	"fmt"
)

// Executed by account deletion's transaction, before removing the account.
// Preserve unknown config fields, unrelated accounts and all test history.
func removeDeletedAccountEvalConfig(ctx context.Context, tx sqlExecutor, accountID int64) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE openai_eval_configs
		SET config = config || jsonb_build_object(
			'accounts', (SELECT COALESCE(jsonb_agg(item ORDER BY position), '[]'::jsonb)
				FROM jsonb_array_elements(CASE WHEN jsonb_typeof(config->'accounts') = 'array' THEN config->'accounts' ELSE '[]'::jsonb END) WITH ORDINALITY AS entries(item, position)
				WHERE item->>'account_id' IS DISTINCT FROM $1::text),
			'account_priority_rules', (SELECT COALESCE(jsonb_agg(item ORDER BY position), '[]'::jsonb)
				FROM jsonb_array_elements(CASE WHEN jsonb_typeof(config->'account_priority_rules') = 'array' THEN config->'account_priority_rules' ELSE '[]'::jsonb END) WITH ORDINALITY AS entries(item, position)
				WHERE item->>'account_id' IS DISTINCT FROM $1::text),
			'bps_accounts', (SELECT COALESCE(jsonb_agg(item ORDER BY position), '[]'::jsonb)
				FROM jsonb_array_elements(CASE WHEN jsonb_typeof(config->'bps_accounts') = 'array' THEN config->'bps_accounts' ELSE '[]'::jsonb END) WITH ORDINALITY AS entries(item, position)
				WHERE item->>'account_id' IS DISTINCT FROM $1::text),
			'revision', COALESCE((config->>'revision')::bigint, 0) + 1), updated_at = NOW()
		WHERE id = 1 AND (
			config->'accounts' @> jsonb_build_array(jsonb_build_object('account_id', $1::bigint)) OR
			config->'account_priority_rules' @> jsonb_build_array(jsonb_build_object('account_id', $1::bigint)) OR
			config->'bps_accounts' @> jsonb_build_array(jsonb_build_object('account_id', $1::bigint)))`, accountID)
	if err != nil {
		return fmt.Errorf("remove deleted account %d from evaluation config: %w", accountID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE openai_eval_schedule_state SET enabled=FALSE, next_run_at=NULL, updated_at=NOW() WHERE account_id=$1`, accountID); err != nil {
		return fmt.Errorf("disable deleted account %d evaluation schedules: %w", accountID, err)
	}
	if affected > 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO openai_eval_audit_events (actor_id, action, payload) VALUES (0, 'deleted_account_removed', jsonb_build_object('account_id', $1::bigint))`, accountID); err != nil {
			return fmt.Errorf("audit deleted account evaluation cleanup: %w", err)
		}
	}
	return nil
}
