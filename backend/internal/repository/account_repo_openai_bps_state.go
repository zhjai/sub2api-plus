package repository

import (
	"context"
	"encoding/json"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// UpdateOpenAIBPSModelState applies one BPS model-state transition while
// holding the account row lock.  State Probe and the BPS forwarder can run on
// different workers, so a process-local mutex is not sufficient: both writers
// must read the current JSON object inside the same database transaction.
func (r *accountRepository) UpdateOpenAIBPSModelState(
	ctx context.Context,
	accountID int64,
	model string,
	transition func(service.OpenAIBPSModelState) (service.OpenAIBPSModelState, bool),
) error {
	if r == nil || r.client == nil || accountID <= 0 || transition == nil {
		return nil
	}

	baseCtx := ctx
	tx := dbent.TxFromContext(ctx)
	ownedTx := false
	if tx == nil {
		var err error
		tx, err = r.client.Tx(ctx)
		if err != nil {
			return err
		}
		ownedTx = true
		ctx = dbent.NewTxContext(ctx, tx)
	}
	if ownedTx {
		defer func() { _ = tx.Rollback() }()
	}
	client := tx.Client()
	key := service.OpenAIBPSModelStateExtraKeyFor(model)

	rows, err := client.QueryContext(ctx, `
		SELECT COALESCE(extra -> ($2::text), '{}'::jsonb)
		FROM accounts
		WHERE id = $1 AND deleted_at IS NULL
		FOR UPDATE
	`, accountID, key)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return service.ErrAccountNotFound
	}
	var raw []byte
	if err := rows.Scan(&raw); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	var current service.OpenAIBPSModelState
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &current); err != nil {
			return err
		}
	}
	next, changed := transition(current)
	if !changed {
		if ownedTx {
			return tx.Commit()
		}
		return nil
	}
	payload, err := json.Marshal(next)
	if err != nil {
		return err
	}
	result, err := client.ExecContext(ctx, `
		UPDATE accounts
		SET extra = jsonb_set(COALESCE(extra, '{}'::jsonb), ARRAY[$2]::text[], $3::jsonb, true),
			updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
	`, accountID, key, string(payload))
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return service.ErrAccountNotFound
	}
	if err := enqueueSchedulerOutbox(ctx, client, service.SchedulerOutboxEventAccountChanged, &accountID, nil, nil); err != nil {
		return err
	}
	if ownedTx {
		if err := tx.Commit(); err != nil {
			return err
		}
		r.syncSchedulerAccountSnapshot(baseCtx, accountID)
	}
	return nil
}

var _ service.OpenAIBPSModelStateRepository = (*accountRepository)(nil)

// UpdateOpenAIBPSAccountState applies an account-scoped BPS state transition
// under the same row lock used by the legacy model-scoped adapter. The
// account-level key is deliberately independent of the requested model: BPS
// degradation is an OAuth account property, while ProbeModel is only a health
// check input.
func (r *accountRepository) UpdateOpenAIBPSAccountState(
	ctx context.Context,
	accountID int64,
	transition func(service.OpenAIBPSAccountState) (service.OpenAIBPSAccountState, bool),
) error {
	if r == nil || r.client == nil || accountID <= 0 || transition == nil {
		return nil
	}

	baseCtx := ctx
	tx := dbent.TxFromContext(ctx)
	ownedTx := false
	if tx == nil {
		var err error
		tx, err = r.client.Tx(ctx)
		if err != nil {
			return err
		}
		ownedTx = true
		ctx = dbent.NewTxContext(ctx, tx)
	}
	if ownedTx {
		defer func() { _ = tx.Rollback() }()
	}
	client := tx.Client()
	key := service.OpenAIBPSAccountStateExtraKey()

	rows, err := client.QueryContext(ctx, `
		SELECT COALESCE(extra, '{}'::jsonb)
		FROM accounts
		WHERE id = $1 AND deleted_at IS NULL
		FOR UPDATE
	`, accountID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return service.ErrAccountNotFound
	}
	var raw []byte
	if err := rows.Scan(&raw); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	var extra map[string]any
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &extra); err != nil {
			return err
		}
	}
	current, legacy := service.ResolveOpenAIBPSAccountState(extra)
	next, changed := transition(current)
	if !changed && !legacy {
		if ownedTx {
			return tx.Commit()
		}
		return nil
	}
	payload, err := json.Marshal(next)
	if err != nil {
		return err
	}
	result, err := client.ExecContext(ctx, `
		UPDATE accounts
		SET extra = jsonb_set(COALESCE(extra, '{}'::jsonb), ARRAY[$2]::text[], $3::jsonb, true),
			updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
	`, accountID, key, string(payload))
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return service.ErrAccountNotFound
	}
	if err := enqueueSchedulerOutbox(ctx, client, service.SchedulerOutboxEventAccountChanged, &accountID, nil, nil); err != nil {
		return err
	}
	if ownedTx {
		if err := tx.Commit(); err != nil {
			return err
		}
		r.syncSchedulerAccountSnapshot(baseCtx, accountID)
	}
	return nil
}

var _ service.OpenAIBPSAccountStateRepository = (*accountRepository)(nil)
