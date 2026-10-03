package dto

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountHardRPMDTOContract(t *testing.T) {
	account := &service.Account{ID: 4, Extra: map[string]any{"rpm_limit": 12, "base_rpm": 7}}
	full := AccountFromService(account)
	require.Equal(t, 12, full.RPMLimit)
	full.RPMCapacity = &service.AccountRPMCapacity{Status: "unavailable"}
	lite := AccountListItemFromAccount(full)
	for _, v := range []any{full, lite} {
		data, err := json.Marshal(v)
		require.NoError(t, err)
		var body map[string]any
		require.NoError(t, json.Unmarshal(data, &body))
		require.Equal(t, float64(12), body["rpm_limit"])
		capacity := body["rpm_capacity"].(map[string]any)
		require.Equal(t, "unavailable", capacity["status"])
		require.Nil(t, capacity["used"])
		require.Nil(t, capacity["remaining"])
		require.Nil(t, capacity["reset_at"])
	}
}
