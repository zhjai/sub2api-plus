package repository

import (
	"context"

	dbaccount "github.com/Wei-Shaw/sub2api/ent/account"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *accountRepository) GetAccountRPMLimit(ctx context.Context, id int64) (int, error) {
	row, err := r.client.Account.Query().Where(dbaccount.IDEQ(id)).Select(dbaccount.FieldExtra).Only(ctx)
	if err != nil {
		return 0, translatePersistenceError(err, service.ErrAccountNotFound, nil)
	}
	return (&service.Account{Extra: row.Extra}).AccountRPMLimit()
}

var _ service.AccountRPMLimitReader = (*accountRepository)(nil)
