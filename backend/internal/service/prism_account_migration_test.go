//go:build unit

package service_test

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/migrations"
	_ "github.com/lib/pq"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPrismAccountPostgresMigrationPaths(t *testing.T) {
	dsn := os.Getenv("PRISM_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("isolated Prism PostgreSQL fixture not configured")
	}
	if !strings.Contains(dsn, "host=/tmp/prism-lifecycle-pg.") {
		t.Fatal("fixture requires dedicated ephemeral socket")
	}
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	for _, path := range []string{"fresh", "upgrade"} {
		t.Run(path, func(t *testing.T) {
			name := fmt.Sprintf("prism_%s_%d", path, time.Now().UnixNano())
			if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("postgres", strings.Replace(dsn, "dbname=postgres", "dbname="+name, 1))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			if path == "fresh" {
				if err := repository.ApplyMigrations(ctx, db); err != nil {
					t.Fatal(err)
				}
				if err := repository.ApplyMigrations(ctx, db); err != nil {
					t.Fatalf("idempotent reapply: %v", err)
				}
				var count int
				if err := db.QueryRow("SELECT count(*) FROM schema_migrations WHERE filename LIKE '241_%' OR filename='245_add_prism_platform.sql'").Scan(&count); err != nil || count != 5 {
					t.Fatalf("same-prefix migrations missing: count=%d err=%v", count, err)
				}
				return
			}
			if _, err := db.Exec("CREATE TABLE user_platform_quotas(platform TEXT); CREATE TABLE composite_model_routes(target_platform TEXT); CREATE TABLE payment_orders(amount NUMERIC(20,2)); INSERT INTO user_platform_quotas VALUES ('openai'),('opencode_go'); INSERT INTO composite_model_routes VALUES ('anthropic'),('opencode_go')"); err != nil {
				t.Fatal(err)
			}
			for _, file := range []string{"241_add_payment_order_bonus_amount.sql", "241_add_typesafe_platform.sql", "245_add_prism_platform.sql"} {
				raw, err := migrations.FS.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.ExecContext(ctx, string(raw)); err != nil {
					t.Fatalf("%s: %v", file, err)
				}
			}
			for _, platform := range []string{"anthropic", "openai", "gemini", "antigravity", "grok", "kimi", "zhipu", "deepseek", "minimax", "opencode_go", "typesafe", "prism"} {
				if _, err := db.Exec("INSERT INTO user_platform_quotas VALUES($1)", platform); err != nil {
					t.Fatalf("quota %s: %v", platform, err)
				}
				if _, err := db.Exec("INSERT INTO composite_model_routes VALUES($1)", platform); err != nil {
					t.Fatalf("route %s: %v", platform, err)
				}
			}
			if _, err := db.Exec("INSERT INTO user_platform_quotas VALUES('unknown')"); err == nil {
				t.Fatal("unknown platform accepted")
			}
			var bonus string
			if err := db.QueryRow("INSERT INTO payment_orders(amount) VALUES(1) RETURNING bonus_amount::text").Scan(&bonus); err != nil || bonus != "0.00" {
				t.Fatalf("bonus default: %s %v", bonus, err)
			}
		})
	}
}
