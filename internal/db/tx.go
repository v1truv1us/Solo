package db

import (
	"context"
	"database/sql"
	"fmt"
)

// WithTxImmediate runs fn inside a BEGIN IMMEDIATE transaction on a pinned connection.
// This ensures all operations in fn use the same underlying SQLite connection.
func WithTxImmediate(db *sql.DB, fn func(conn *sql.Conn) error) error {
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("getting connection: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin immediate: %w", err)
	}

	if err := fn(conn); err != nil {
		conn.ExecContext(ctx, "ROLLBACK")
		return err
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		conn.ExecContext(ctx, "ROLLBACK")
		return fmt.Errorf("commit: %w", err)
	}

	return nil
}

// WithTxImmediateResult runs fn inside a BEGIN IMMEDIATE transaction and returns a result.
func WithTxImmediateResult[T any](db *sql.DB, fn func(conn *sql.Conn) (T, error)) (T, error) {
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		var zero T
		return zero, fmt.Errorf("getting connection: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		var zero T
		return zero, fmt.Errorf("begin immediate: %w", err)
	}

	result, err := fn(conn)
	if err != nil {
		conn.ExecContext(ctx, "ROLLBACK")
		return result, err
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		conn.ExecContext(ctx, "ROLLBACK")
		var zero T
		return zero, fmt.Errorf("commit: %w", err)
	}

	return result, nil
}
