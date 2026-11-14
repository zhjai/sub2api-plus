package repository

import (
	"context"
	"encoding/json"
	"fmt"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbaccountgroup "github.com/Wei-Shaw/sub2api/ent/accountgroup"
	dbgroup "github.com/Wei-Shaw/sub2api/ent/group"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"strconv"
	"strings"
	"time"
)

// Compare-and-swap claims persist before I/O and survive process restarts.
func (r *accountRepository) CompareAndSwapExcelBPSExtra(ctx context.Context, expected *service.Account, updates map[string]any) (bool, error) {
	if scoped := r.accountTransactionScope(ctx); scoped != r {
		return scoped.CompareAndSwapExcelBPSExtra(ctx, expected, updates)
	}
	scope := map[string]any{}
	for k, v := range expected.Extra {
		if strings.HasPrefix(k, "openai_excel_bps") {
			scope[k] = v
		}
	}
	extra, err := json.Marshal(scope)
	if err != nil {
		return false, err
	}
	credentials, err := json.Marshal(normalizeJSONMap(service.ExcelBPSPersistenceCredentials(expected)))
	if err != nil {
		return false, err
	}
	// The move request is an operation parameter. Only a successful group move
	// writes moved_at; an unavailable target must not claim that it moved.
	moveAt := updates[service.ExcelBPS403MoveRequestedAtKey]
	if moveAt == nil {
		moveAt = updates[service.ExcelBPS403DisabledAtKey]
	}
	persisted := make(map[string]any, len(updates))
	for key, value := range updates {
		if key != service.ExcelBPS403MoveRequestedAtKey {
			persisted[key] = value
		}
	}
	patch, err := json.Marshal(persisted)
	if err != nil {
		return false, err
	}
	client := clientFromContext(ctx, r.client)
	var tx *dbent.Tx
	if dbent.TxFromContext(ctx) == nil {
		tx, err = r.client.Tx(ctx)
		if err != nil {
			return false, err
		}
		defer tx.Rollback()
		ctx = dbent.NewTxContext(ctx, tx)
		client = tx.Client()
	}
	result, err := client.ExecContext(ctx, `UPDATE accounts SET extra = COALESCE(extra,'{}'::jsonb) || $1::jsonb, updated_at=NOW() WHERE id=$2 AND deleted_at IS NULL AND COALESCE((SELECT jsonb_object_agg(key,value) FROM jsonb_each(COALESCE(extra,'{}'::jsonb)) WHERE key LIKE 'openai_excel_bps%'),'{}'::jsonb)=$3::jsonb AND credentials=$4::jsonb AND (($1::jsonb ? 'openai_excel_bps_grant_refresh_until') OR (schedulable=true AND status='active'))`, string(patch), expected.ID, string(extra), string(credentials))
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil || n == 0 {
		return false, err
	}
	groupID, validTarget := excelBPSGroupID(expected.Extra[service.ExcelBPS403TargetGroupIDKey])
	if moveAt != nil && expected.Extra[service.ExcelBPSAutoMoveOn403Key] == true && validTarget {
		group, groupErr := client.Group.Query().Where(dbgroup.IDEQ(groupID), dbgroup.DeletedAtIsNil(), dbgroup.StatusEQ(service.StatusActive), dbgroup.PlatformIn(service.PlatformOpenAI, service.PlatformComposite)).ForShare().Only(ctx)
		if groupErr != nil && !dbent.IsNotFound(groupErr) {
			return false, fmt.Errorf("BPS auto move target unavailable: %w", groupErr)
		}
		if group != nil {
			oldBindings, groupErr := client.AccountGroup.Query().Where(dbaccountgroup.AccountIDEQ(expected.ID)).All(ctx)
			if groupErr != nil {
				return false, groupErr
			}
			if _, groupErr = client.AccountGroup.Delete().Where(dbaccountgroup.AccountIDEQ(expected.ID)).Exec(ctx); groupErr != nil {
				return false, groupErr
			}
			if _, groupErr = client.AccountGroup.Create().SetAccountID(expected.ID).SetGroupID(group.ID).SetPriority(1).Save(ctx); groupErr != nil {
				return false, groupErr
			}
			moved := map[string]any{"openai_excel_bps_403_moved_at": moveAt, "openai_excel_bps_403_moved_group_id": group.ID}
			payload, _ := json.Marshal(moved)
			if _, groupErr = client.ExecContext(ctx, `UPDATE accounts SET extra=extra || $1::jsonb WHERE id=$2`, string(payload), expected.ID); groupErr != nil {
				return false, groupErr
			}
			ids := []int64{group.ID}
			for _, binding := range oldBindings {
				ids = append(ids, binding.GroupID)
			}
			if groupErr = enqueueSchedulerOutbox(ctx, client, service.SchedulerOutboxEventAccountGroupsChanged, &expected.ID, nil, buildSchedulerGroupPayload(ids)); groupErr != nil {
				return false, groupErr
			}
		}
	}
	if err = enqueueSchedulerOutbox(ctx, client, service.SchedulerOutboxEventAccountChanged, &expected.ID, nil, nil); err != nil {
		return false, err
	}
	if tx != nil {
		if err = tx.Commit(); err != nil {
			return false, err
		}
		r.syncSchedulerAccountSnapshot(context.Background(), expected.ID)
	}
	return true, nil
}
func excelBPSGroupID(raw any) (int64, bool) {
	var text string
	switch value := raw.(type) {
	case int:
		return int64(value), value > 0
	case int64:
		return value, value > 0
	case float64:
		id := int64(value)
		return id, value == float64(id) && id > 0
	case json.Number:
		text = value.String()
	default:
		return 0, false
	}
	id, err := strconv.ParseInt(text, 10, 64)
	return id, err == nil && id > 0
}

func (r *accountRepository) ReplaceExcelBPSGrant(ctx context.Context, id int64, previous, next map[string]any) (bool, error) {
	old, err := json.Marshal(previous)
	if err != nil {
		return false, err
	}
	updated, err := json.Marshal(next)
	if err != nil {
		return false, err
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	client := tx.Client()
	result, err := client.ExecContext(ctx, `UPDATE accounts SET credentials=jsonb_set(credentials,'{excel_bps_credentials}',$1::jsonb,true),extra=COALESCE(extra,'{}'::jsonb)-'openai_excel_bps_grant_refresh_until',updated_at=$2 WHERE id=$3 AND deleted_at IS NULL AND credentials->'excel_bps_credentials'=$4::jsonb`, string(updated), time.Now(), id, string(old))
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil || n == 0 {
		return false, err
	}
	if err = enqueueSchedulerOutbox(ctx, client, service.SchedulerOutboxEventAccountChanged, &id, nil, nil); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	r.syncSchedulerAccountSnapshot(ctx, id)
	return true, nil
}
