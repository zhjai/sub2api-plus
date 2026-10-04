package service

import (
	"context"
	"errors"
)

type AccountLoadPeekReader interface {
	PeekAccountsLoadBatch(context.Context, []AccountWithConcurrency) (map[int64]*AccountLoadInfo, error)
}

func (s *ConcurrencyService) PeekAccountsLoadBatch(ctx context.Context, accounts []AccountWithConcurrency) (map[int64]*AccountLoadInfo, error) {
	if s != nil {
		if reader, ok := s.cache.(AccountLoadPeekReader); ok {
			return reader.PeekAccountsLoadBatch(ctx, accounts)
		}
	}
	return nil, errors.New("read-only account load is unavailable")
}
