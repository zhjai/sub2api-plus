//go:build unit

package service

import (
	"context"
	"database/sql"
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	_ "modernc.org/sqlite"
	"testing"
)

type prismAtomicAdmin struct{ AdminService }

func (prismAtomicAdmin) ValidateAccountGroupBindings(context.Context, []int64) error { return nil }
func (prismAtomicAdmin) CheckMixedChannelRisk(context.Context, int64, string, []int64) error {
	return nil
}

func TestPrismAccountAtomicCreateRollbackOnOutboxFailure(t *testing.T) {
	db, err := sql.Open("sqlite", "file:prism_atomic_rollback?mode=memory&cache=shared&_fk=1")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatal(err)
	}
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(entsql.OpenDB(dialect.SQLite, db))))
	defer client.Close()
	s := NewPrismAccountService(prismAtomicAdmin{})
	ctx := context.Background()
	tx, err := client.Tx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// No scheduler_outbox table: force a failure after the account insert. The
	// caller rolls back its transaction, leaving neither an account nor bindings.
	_, err = s.createAtomic(ctx, tx, &CreateAccountInput{Name: "Prism fixture", Platform: "prism", Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "fixture"}, Concurrency: 2, Priority: 50})
	if err == nil {
		t.Fatal("expected outbox persistence failure")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	count, err := client.Account.Query().Count(ctx)
	if err != nil || count != 0 {
		t.Fatalf("partial account escaped rollback: count=%d err=%v", count, err)
	}
	if _, err := db.Exec("CREATE TABLE scheduler_outbox(event_type TEXT,account_id INTEGER,payload BLOB)"); err != nil {
		t.Fatal(err)
	}
	tx, err = client.Tx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	created, err := s.createAtomic(ctx, tx, &CreateAccountInput{Name: "Prism fixture", Platform: "prism", Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "fixture"}, Concurrency: 2, Priority: 50})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if created.ID == 0 {
		t.Fatal("missing account id")
	}
	var events int
	if err := db.QueryRow("SELECT COUNT(*) FROM scheduler_outbox").Scan(&events); err != nil || events != 1 {
		t.Fatalf("outbox event missing: %d %v", events, err)
	}
}
