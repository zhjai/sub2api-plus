package service

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
)

// newExcelBPSPersistentCaches gives each request its own error state and context.
// The injected GatewayCache supplies the shared store as an optional interface;
// it is independent of the legacy account activation state in openai_bps_state.go.
func (s *OpenAIGatewayService) newExcelBPSPersistentCaches(ctx context.Context) (*basispoints.ReplayCache, *basispoints.CatalogCache) {
	var store basispoints.StateStore = unavailableExcelBPSStateStore{}
	if s != nil {
		if shared, ok := s.cache.(basispoints.StateStore); ok && shared != nil {
			store = shared
		}
	}
	return basispoints.NewPersistentCaches(ctx, store)
}

// A missing shared store must never silently downgrade to process-local state.
type unavailableExcelBPSStateStore struct{}

func (unavailableExcelBPSStateStore) LoadBPSState(context.Context, string, string) ([]byte, uint64, error) {
	return nil, 0, basispoints.ErrStateUnavailable
}

func (unavailableExcelBPSStateStore) SaveBPSState(context.Context, string, string, *uint64, []byte) (bool, error) {
	return false, basispoints.ErrStateUnavailable
}
