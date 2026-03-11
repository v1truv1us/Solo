package solo

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"syscall"
)

type zombieSession struct {
	SessionID     string
	TaskID        string
	ReservationID string
	PID           int
}

func lazyZombieScan(db *sql.DB) {
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return
	}
	defer conn.Close()
	if err := applyConnPragmas(ctx, conn, 0); err != nil {
		return
	}
	defer func() {
		_ = applyConnPragmas(ctx, conn, 5000)
	}()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		if isBusy(err) {
			return
		}
		return
	}
	err = func(conn *sql.Conn) error {
		rows, err := conn.QueryContext(ctx, `SELECT s.id, s.task_id, s.reservation_id, s.agent_pid
			FROM sessions s
			JOIN reservations r ON r.id=s.reservation_id
			WHERE s.ended_at IS NULL AND s.agent_pid IS NOT NULL AND r.active=1`)
		if err != nil {
			if isBusy(err) {
				return nil
			}
			return err
		}
		defer rows.Close()
		toRecover := []zombieSession{}
		for rows.Next() {
			var z zombieSession
			if err := rows.Scan(&z.SessionID, &z.TaskID, &z.ReservationID, &z.PID); err != nil {
				return err
			}
			if isProcessDead(z.PID) {
				toRecover = append(toRecover, z)
			}
		}
		for _, z := range toRecover {
			if err := recoverZombieSession(ctx, conn, z); err != nil {
				return err
			}
		}
		expRows, err := conn.QueryContext(ctx, `SELECT id, task_id FROM reservations WHERE active=1 AND expires_at < strftime('%Y-%m-%dT%H:%M:%fZ','now')`)
		if err != nil {
			return err
		}
		defer expRows.Close()
		type exp struct{ resID, taskID string }
		expired := []exp{}
		for expRows.Next() {
			var e exp
			if err := expRows.Scan(&e.resID, &e.taskID); err != nil {
				return err
			}
			expired = append(expired, e)
		}
		for _, e := range expired {
			if err := recoverExpiredReservation(ctx, conn, e.resID, e.taskID); err != nil {
				return err
			}
		}
		return nil
	}(conn)
	if err != nil {
		_, _ = conn.ExecContext(ctx, "ROLLBACK")
		return
	}
	_, _ = conn.ExecContext(ctx, "COMMIT")
}

func recoverExpiredReservation(ctx context.Context, conn *sql.Conn, reservationID, taskID string) error {
	var prevStatus string
	var version int
	if err := conn.QueryRowContext(ctx, `SELECT status, version FROM tasks WHERE id=?`, taskID).Scan(&prevStatus, &version); err != nil {
		return err
	}
	var sessionID sql.NullString
	_ = conn.QueryRowContext(ctx, `SELECT id FROM sessions WHERE reservation_id=? AND ended_at IS NULL`, reservationID).Scan(&sessionID)
	if sessionID.Valid {
		if _, err := conn.ExecContext(ctx, `UPDATE sessions SET ended_at=strftime('%Y-%m-%dT%H:%M:%fZ','now'), result='interrupted' WHERE id=? AND ended_at IS NULL`, sessionID.String); err != nil {
			return err
		}
	}
	if _, err := conn.ExecContext(ctx, `UPDATE reservations
		SET active=0, released_at=strftime('%Y-%m-%dT%H:%M:%fZ','now'), release_reason='expired'
		WHERE id=? AND active=1`, reservationID); err != nil {
		return err
	}
	res, err := conn.ExecContext(ctx, `UPDATE tasks SET status='ready', version=version+1, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')
		WHERE id=? AND version=? AND status IN ('active','in_progress')`, taskID, version)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil
	}
	_, err = conn.ExecContext(ctx, `INSERT INTO recovery_records (task_id, session_id, reason, previous_status, recovered_to, worktree_state, uncommitted_files, diagnostic_json)
		VALUES (?, ?, 'reservation_expired', ?, 'ready', NULL, '[]', '{}')`, taskID, nullIfInvalid(sessionID), prevStatus)
	if err != nil {
		return err
	}
	return writeAudit(ctx, conn, taskID, "task.recovered", "system", "zombie_scan", map[string]any{"status": prevStatus}, map[string]any{"status": "ready", "reason": "reservation_expired"})
}

func isBusy(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "busy")
}

func recoverZombieSession(ctx context.Context, conn *sql.Conn, z zombieSession) error {
	var prevStatus string
	var version int
	if err := conn.QueryRowContext(ctx, `SELECT status, version FROM tasks WHERE id=?`, z.TaskID).Scan(&prevStatus, &version); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, `UPDATE sessions SET ended_at=strftime('%Y-%m-%dT%H:%M:%fZ','now'), result='interrupted' WHERE id=? AND ended_at IS NULL`, z.SessionID); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, `UPDATE reservations SET active=0, released_at=strftime('%Y-%m-%dT%H:%M:%fZ','now'), release_reason='recovered' WHERE id=? AND active=1`, z.ReservationID); err != nil {
		return err
	}
	resTask, err := conn.ExecContext(ctx, `UPDATE tasks SET status='ready', version=version+1, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=? AND version=? AND status IN ('active','in_progress')`, z.TaskID, version)
	if err != nil {
		return err
	}
	n, _ := resTask.RowsAffected()
	if n == 0 {
		return nil
	}
	res, err := conn.ExecContext(ctx, `INSERT INTO recovery_records (task_id, session_id, reason, previous_status, recovered_to, worktree_state, uncommitted_files, diagnostic_json)
		VALUES (?, ?, 'crash_detected', ?, 'ready', NULL, '[]', ?)`, z.TaskID, z.SessionID, prevStatus, mustJSON(map[string]any{"dead_pid": z.PID}))
	if err != nil {
		return err
	}
	recID, _ := res.LastInsertId()
	return writeAudit(ctx, conn, z.TaskID, "task.recovered", "system", "zombie_scan", map[string]any{"status": prevStatus}, map[string]any{"status": "ready", "record": recID})
}

func nullIfInvalid(v sql.NullString) any {
	if v.Valid {
		return v.String
	}
	return nil
}

func isProcessDead(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return true
	}
	err = process.Signal(syscall.Signal(0))
	return errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH)
}
