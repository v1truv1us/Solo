package db_test

import (
	"database/sql"
	"context"
	"fmt"
	"testing"

	"github.com/v1truv1us/solo/internal/db"
)

func TestWithTxImmediate(t *testing.T) {
	database := testDB(t)

	err := db.WithTxImmediate(database, func(conn *sql.Conn) error {
		ctx := context.Background()
		_, err := conn.ExecContext(ctx, "INSERT INTO config (key, value) VALUES ('test_key', 'test_value')")
		return err
	})
	if err != nil {
		t.Fatalf("tx: %v", err)
	}

	// Verify committed
	var val string
	database.QueryRow("SELECT value FROM config WHERE key = 'test_key'").Scan(&val)
	if val != "test_value" {
		t.Errorf("expected test_value, got %q", val)
	}
}

func TestWithTxImmediateRollback(t *testing.T) {
	database := testDB(t)

	err := db.WithTxImmediate(database, func(conn *sql.Conn) error {
		ctx := context.Background()
		conn.ExecContext(ctx, "INSERT INTO config (key, value) VALUES ('rollback_key', 'value')")
		return fmt.Errorf("intentional error")
	})
	if err == nil {
		t.Error("expected error")
	}

	// Verify rolled back
	var count int
	database.QueryRow("SELECT COUNT(*) FROM config WHERE key = 'rollback_key'").Scan(&count)
	if count != 0 {
		t.Error("expected rollback - row should not exist")
	}
}

func TestWithTxImmediateResult(t *testing.T) {
	database := testDB(t)

	result, err := db.WithTxImmediateResult(database, func(conn *sql.Conn) (string, error) {
		ctx := context.Background()
		conn.ExecContext(ctx, "INSERT INTO config (key, value) VALUES ('result_key', 'result_value')")
		return "success", nil
	})
	if err != nil {
		t.Fatalf("tx: %v", err)
	}
	if result != "success" {
		t.Errorf("expected 'success', got %q", result)
	}
}
