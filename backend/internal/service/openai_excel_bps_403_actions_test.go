package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type bps403ActionsRepo struct {
	bps403Repo
	patches []map[string]any
	changed bool
}

func (r *bps403ActionsRepo) CompareAndSwapExcelBPSExtra(_ context.Context, _ *Account, patch map[string]any) (bool, error) {
	r.patches = append(r.patches, shallowCopyMap(patch))
	return r.changed, nil
}

func TestExcelBPS403DisableAndMoveAreIndependent(t *testing.T) {
	for _, dual := range []bool{false, true} {
		for _, actions := range []struct{ disable, move bool }{{true, false}, {false, true}, {true, true}} {
			t.Run(fmt.Sprintf("dual_%t_disable_%t_move_%t", dual, actions.disable, actions.move), func(t *testing.T) {
				a := excelBPSAccount(t)
				if dual {
					a = dualBPSEvalFixture(t).ExcelBPSCredentialAccount()
				}
				a.Status, a.Schedulable = StatusActive, true
				a.Extra = map[string]any{ExcelBPSEnabledKey: true, ExcelBPSAutoDisableOn403Key: actions.disable, ExcelBPSAutoMoveOn403Key: actions.move, ExcelBPS403TargetGroupIDKey: json.Number("71")}
				repo := &bps403ActionsRepo{changed: true}
				svc := &RateLimitService{accountRepo: repo}
				require.True(t, svc.handleExcelBPS403(context.Background(), a, []byte(`{"error":{"code":"permission_denied"}}`), "gpt-6-astra"))
				require.Len(t, repo.patches, 1)
				patch := repo.patches[0]
				require.Equal(t, actions.disable, patch[ExcelBPS403DisabledAtKey] != nil)
				require.Equal(t, actions.disable, patch[ExcelBPS403DisabledReasonKey] != nil)
				require.Equal(t, actions.disable, patch[ExcelBPS403ProbeModelKey] != nil)
				require.Equal(t, actions.move, patch["openai_excel_bps_403_move_requested_at"] != nil)
				require.True(t, a.Schedulable)
				require.Equal(t, StatusActive, a.Status)
				require.Zero(t, repo.temps)
				if !actions.disable {
					require.True(t, a.IsExcelBPSEnabled())
					require.False(t, a.ExcelBPS403RecoveryPending())
					require.False(t, a.ExcelBPS403RecoveryDue(time.Now().Add(2*time.Hour)))
					require.NotContains(t, a.Extra, ExcelBPS403LastProbeAtKey)
				}
				require.NotContains(t, a.Extra, "openai_excel_bps_403_move_requested_at", "move intent is not durable account metadata")
			})
		}
	}
}

func TestExcelBPS403ActionsCASConflictDoesNotMutateSnapshot(t *testing.T) {
	a := excelBPSAccount(t)
	a.Status, a.Schedulable = StatusActive, true
	a.Extra = map[string]any{ExcelBPSAutoDisableOn403Key: false, ExcelBPSAutoMoveOn403Key: true, ExcelBPS403TargetGroupIDKey: 71}
	original := shallowCopyMap(a.Extra)
	repo := &bps403ActionsRepo{changed: false}
	svc := &RateLimitService{accountRepo: repo}
	require.False(t, svc.handleExcelBPS403(context.Background(), a, []byte(`{"error":{"code":"forbidden"}}`), "gpt-6-astra"))
	require.Len(t, repo.patches, 1)
	require.Equal(t, original, a.Extra)
	require.True(t, a.Schedulable)
	require.Nil(t, a.TempUnschedulableUntil)
}
