package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/stretchr/testify/require"
)

func TestExcelBPSPersistentCachesMissingStoreFailsClosed(t *testing.T) {
	for _, s := range []*OpenAIGatewayService{nil, {}, {cache: struct{ GatewayCache }{}}} {
		replay, catalog := s.newExcelBPSPersistentCaches(context.Background())
		_, _, err := basispoints.PrepareWithCatalog([]byte(`{"model":"gpt-6.1-sol","input":"hello"}`), "scope", replay, catalog)
		require.ErrorIs(t, err, basispoints.ErrStateUnavailable)
	}
}

type excelBPSStateCacheStub struct {
	GatewayCache
	raw     []byte
	version uint64
}

func (s *excelBPSStateCacheStub) LoadBPSState(ctx context.Context, _, _ string) ([]byte, uint64, error) {
	return s.raw, s.version, ctx.Err()
}

func (s *excelBPSStateCacheStub) SaveBPSState(ctx context.Context, _, _ string, expected *uint64, raw []byte) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if expected != nil && *expected != s.version {
		return false, nil
	}
	s.raw = append([]byte(nil), raw...)
	s.version++
	return true, nil
}

func TestExcelBPSPersistentCachesShareStoreButIsolateRequestFailure(t *testing.T) {
	s := &OpenAIGatewayService{cache: &excelBPSStateCacheStub{}}
	ctx, cancel := context.WithCancel(context.Background())
	failedReplay, failedCatalog := s.newExcelBPSPersistentCaches(ctx)
	cancel()
	source := []byte(`{"model":"gpt-6.1-sol","input":"hello","tools":[{"type":"custom","name":"retained_tool"}]}`)
	_, _, err := basispoints.PrepareWithCatalog(source, "scope", failedReplay, failedCatalog)
	require.ErrorIs(t, err, basispoints.ErrStateUnavailable)
	replay, catalog := s.newExcelBPSPersistentCaches(context.Background())
	require.NotSame(t, failedReplay, replay)
	require.NotSame(t, failedCatalog, catalog)
	_, _, err = basispoints.PrepareWithCatalog(source, "scope", replay, catalog)
	require.NoError(t, err)
	replay, catalog = s.newExcelBPSPersistentCaches(context.Background())
	body, _, err := basispoints.PrepareWithCatalog([]byte(`{"model":"gpt-6.1-sol","input":"continue"}`), "scope", replay, catalog)
	require.NoError(t, err)
	require.Contains(t, string(body), "retained_tool")
	require.ErrorIs(t, failedCatalog.Err(), basispoints.ErrStateUnavailable)
}
