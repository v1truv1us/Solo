package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/google/uuid"
	"github.com/v1truv1us/solo/internal/output"
)

// Session represents a session record.
type Session struct {
	ID            string            `json:"id"`
	TaskID        string            `json:"task_id"`
	ReservationID string            `json:"reservation_id"`
	WorkerID      string            `json:"worker_id"`
	AgentPID      *int              `json:"agent_pid,omitempty"`
	WorktreePath  *string           `json:"worktree_path,omitempty"`
	Branch        *string           `json:"branch,omitempty"`
	StartedAt     string            `json:"started_at"`
	EndedAt       *string           `json:"ended_at"`
	Result        *string           `json:"result"`
	ResultDetail  *string           `json:"result_detail,omitempty"`
	ExitCode      *int              `json:"exit_code,omitempty"`
	Notes         string            `json:"notes"`
	Commits       []json.RawMessage `json:"commits"`
	FilesChanged  []string          `json:"files_changed"`
}

// Reservation represents a reservation record.
type Reservation struct {
	ID            string  `json:"id"`
	TaskID        string  `json:"task_id"`
	WorkerID      string  `json:"worker_id"`
	Active        bool    `json:"active"`
	ReservedAt    string  `json:"reserved_at"`
	ExpiresAt     string  `json:"expires_at"`
	TTLSec        int     `json:"ttl_sec"`
	ReleasedAt    *string `json:"released_at,omitempty"`
	ReleaseReason *string `json:"release_reason,omitempty"`
	WorktreePath  *string `json:"worktree_path,omitempty"`
	MachineID     string  `json:"machine_id"`
}

// SessionStartResult holds the result of starting a session.
type SessionStartResult struct {
	SessionID     string `json:"session_id"`
	ReservationID string `json:"reservation_id"`
	TaskID        string `json:"task_id"`
	WorktreePath  string `json:"worktree_path"`
	Branch        string `json:"branch"`
	ExpiresAt     string `json:"expires_at"`
	TaskVersion   int    `json:"-"` // used internally for worktree creation
}

// StartSession performs the atomic reservation per spec §6.2.
// Returns the session info. Worktree creation happens after commit (caller's responsibility).
func StartSession(db *sql.DB, taskID, workerID string, pid int, ttlSec int) (*SessionStartResult, error) {
	if strings.TrimSpace(workerID) == "" {
		return nil, output.NewError(output.ErrInvalidArgument, "worker is required", false, "")
	}
	if pid <= 0 {
		pid = os.Getpid()
	}
	if ttlSec <= 0 {
		val, err := GetConfig(db, "default_ttl_sec")
		if err != nil {
			ttlSec = 3600
		} else {
			ttlSec, _ = strconv.Atoi(val)
			if ttlSec <= 0 {
				ttlSec = 3600
			}
		}
	}

	// Check max TTL
	maxTTLStr, _ := GetConfig(db, "max_ttl_sec")
	maxTTL, _ := strconv.Atoi(maxTTLStr)
	if maxTTL > 0 && ttlSec > maxTTL {
		ttlSec = maxTTL
	}

	machineID, _ := GetConfig(db, "machine_id")
	if machineID == "" {
		machineID = "default"
	}

	return WithTxImmediateResult(db, func(conn *sql.Conn) (*SessionStartResult, error) {
		ctx := context.Background()

		// Step 1: Verify task exists and is in 'ready' status
		var taskVersion int
		var taskStatus string
		err := conn.QueryRowContext(ctx, "SELECT status, version FROM tasks WHERE id = ?", taskID).Scan(&taskStatus, &taskVersion)
		if err == sql.ErrNoRows {
			return nil, output.NewError(output.ErrTaskNotFound,
				fmt.Sprintf("task %q not found", taskID), false, "Check ID with solo task list")
		}
		if err != nil {
			return nil, fmt.Errorf("querying task: %w", err)
		}
		if taskStatus != "ready" {
			return nil, output.NewError(output.ErrTaskNotReady,
				fmt.Sprintf("task %s status is '%s', must be 'ready'", taskID, taskStatus),
				false, "Check status with solo task show")
		}

		// Step 2: Insert reservation (unique index prevents double-reservation)
		reservationID := uuid.New().String()
		_, err = conn.ExecContext(ctx, `
			INSERT INTO reservations (id, task_id, worker_id, expires_at, ttl_sec, machine_id)
			VALUES (?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now', '+' || ? || ' seconds'), ?, ?)`,
			reservationID, taskID, workerID, ttlSec, ttlSec, machineID)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed") || strings.Contains(err.Error(), "idx_reservations_active_task") {
				return nil, output.NewError(output.ErrTaskLocked,
					fmt.Sprintf("task %s already has an active reservation", taskID),
					false, "Wait for agent to finish, or use solo task recover")
			}
			return nil, fmt.Errorf("inserting reservation: %w", err)
		}

		// Step 3: Create session record
		sessionID := uuid.New().String()
		_, err = conn.ExecContext(ctx, `
			INSERT INTO sessions (id, task_id, reservation_id, worker_id, agent_pid)
			VALUES (?, ?, ?, ?, ?)`,
			sessionID, taskID, reservationID, workerID, pid)
		if err != nil {
			return nil, fmt.Errorf("inserting session: %w", err)
		}

		// Step 4: Transition task status ready -> in_progress with OCC
		result, err := conn.ExecContext(ctx, `
			UPDATE tasks SET status = 'in_progress', version = version + 1,
				updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
			WHERE id = ? AND version = ?`, taskID, taskVersion)
		if err != nil {
			return nil, fmt.Errorf("updating task status: %w", err)
		}
		rowsAffected, _ := result.RowsAffected()
		if rowsAffected == 0 {
			return nil, output.NewError(output.ErrVersionConflict,
				"task version changed during session start", true, "Re-read current version, retry")
		}

		// Step 5: Write audit event
		conn.ExecContext(ctx, `INSERT INTO audit_events (task_id, event_type, actor_type, actor_id, old_value_json, new_value_json)
			VALUES (?, 'session.start', 'agent', ?, ?, ?)`,
			taskID, workerID,
			fmt.Sprintf(`{"status":"ready","version":%d}`, taskVersion),
			fmt.Sprintf(`{"status":"in_progress","version":%d,"session_id":"%s","reservation_id":"%s"}`, taskVersion+1, sessionID, reservationID))

		// Get expires_at
		var expiresAt string
		conn.QueryRowContext(ctx, "SELECT expires_at FROM reservations WHERE id = ?", reservationID).Scan(&expiresAt)

		return &SessionStartResult{
			SessionID:     sessionID,
			ReservationID: reservationID,
			TaskID:        taskID,
			ExpiresAt:     expiresAt,
			TaskVersion:   taskVersion + 1,
		}, nil
	})
}

// UpdateSessionWorktree updates session and reservation with worktree info after creation.
func UpdateSessionWorktree(db *sql.DB, sessionID, reservationID, worktreePath, branch string) error {
	return WithTxImmediate(db, func(conn *sql.Conn) error {
		ctx := context.Background()
		conn.ExecContext(ctx, "UPDATE sessions SET worktree_path = ?, branch = ? WHERE id = ?", worktreePath, branch, sessionID)
		conn.ExecContext(ctx, "UPDATE reservations SET worktree_path = ? WHERE id = ?", worktreePath, reservationID)
		return nil
	})
}

// CompensateSessionStart rolls back a session start if worktree creation fails.
func CompensateSessionStart(db *sql.DB, sessionID, reservationID, taskID string, previousVersion int) error {
	return WithTxImmediate(db, func(conn *sql.Conn) error {
		ctx := context.Background()
		conn.ExecContext(ctx, "DELETE FROM sessions WHERE id = ?", sessionID)
		conn.ExecContext(ctx, "DELETE FROM reservations WHERE id = ?", reservationID)
		conn.ExecContext(ctx, "UPDATE tasks SET status = 'ready', version = version + 1 WHERE id = ?", taskID)
		conn.ExecContext(ctx, `INSERT INTO audit_events (task_id, event_type, actor_type, actor_id, new_value_json)
			VALUES (?, 'session.start_rollback', 'system', 'cli', '{"reason":"worktree_creation_failed"}')`, taskID)
		return nil
	})
}

// EndSessionParams holds parameters for ending a session.
type EndSessionParams struct {
	TaskID         string
	Result         string // completed, failed, interrupted, abandoned
	Notes          string
	Commits        string // JSON array
	FilesChanged   string // JSON array
	StatusOverride string // optional: for --status done
}

// EndSessionResult holds the result of ending a session.
type EndSessionResult struct {
	SessionID  string `json:"session_id"`
	TaskID     string `json:"task_id"`
	Result     string `json:"result"`
	TaskStatus string `json:"task_status"`
	EndedAt    string `json:"ended_at"`
}

// EndSession ends the active session for a task per spec §6.5.
func EndSession(db *sql.DB, p EndSessionParams) (*EndSessionResult, error) {
	validResults := map[string]bool{"completed": true, "failed": true, "interrupted": true, "abandoned": true}
	if !validResults[p.Result] {
		return nil, output.NewError(output.ErrInvalidArgument,
			fmt.Sprintf("invalid result %q; valid: completed, failed, interrupted, abandoned", p.Result),
			false, "")
	}

	return WithTxImmediateResult(db, func(conn *sql.Conn) (*EndSessionResult, error) {
		ctx := context.Background()

		// Find active session for this task
		var sessionID, reservationID, workerID string
		var taskVersion int
		err := conn.QueryRowContext(ctx, `
			SELECT s.id, s.reservation_id, s.worker_id, t.version
			FROM sessions s
			JOIN tasks t ON t.id = s.task_id
			WHERE s.task_id = ? AND s.ended_at IS NULL`, p.TaskID,
		).Scan(&sessionID, &reservationID, &workerID, &taskVersion)
		if err == sql.ErrNoRows {
			return nil, output.NewError(output.ErrNoActiveSession,
				fmt.Sprintf("no active session for task %s", p.TaskID),
				false, "Check solo session list --task "+p.TaskID)
		}
		if err != nil {
			return nil, fmt.Errorf("querying session: %w", err)
		}

		// End session
		commits := p.Commits
		if commits == "" {
			commits = "[]"
		}
		filesChanged := p.FilesChanged
		if filesChanged == "" {
			filesChanged = "[]"
		}

		_, err = conn.ExecContext(ctx, `
			UPDATE sessions SET
				ended_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
				result = ?,
				notes = ?,
				commits = ?,
				files_changed = ?
			WHERE id = ? AND ended_at IS NULL`,
			p.Result, p.Notes, commits, filesChanged, sessionID)
		if err != nil {
			return nil, fmt.Errorf("ending session: %w", err)
		}

		// Release reservation
		releaseReason := p.Result
		if releaseReason == "interrupted" || releaseReason == "abandoned" {
			releaseReason = "manual"
		}
		conn.ExecContext(ctx, `
			UPDATE reservations SET
				active = 0,
				released_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
				release_reason = ?
			WHERE id = ? AND active = 1`, releaseReason, reservationID)

		// Automatic status transition per spec §5.2
		var newStatus string
		switch p.Result {
		case "completed":
			if p.StatusOverride == "done" {
				newStatus = "done"
			} else {
				newStatus = "in_review"
			}
		case "failed":
			newStatus = "" // no transition, stays in_progress
		case "handoff", "interrupted", "abandoned":
			newStatus = "ready"
		}

		if newStatus != "" {
			result, err := conn.ExecContext(ctx, `
				UPDATE tasks SET status = ?, version = version + 1,
					updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
				WHERE id = ? AND version = ?`, newStatus, p.TaskID, taskVersion)
			if err != nil {
				return nil, fmt.Errorf("updating task status: %w", err)
			}
			rows, _ := result.RowsAffected()
			if rows == 0 {
				return nil, output.NewError(output.ErrVersionConflict,
					"task version changed during session end", true, "Re-read current version, retry")
			}
		}

		// Get final status
		var taskStatus string
		conn.QueryRowContext(ctx, "SELECT status FROM tasks WHERE id = ?", p.TaskID).Scan(&taskStatus)

		// Get ended_at
		var endedAt string
		conn.QueryRowContext(ctx, "SELECT ended_at FROM sessions WHERE id = ?", sessionID).Scan(&endedAt)

		// Audit
		conn.ExecContext(ctx, `INSERT INTO audit_events (task_id, event_type, actor_type, actor_id, old_value_json, new_value_json)
			VALUES (?, 'session.end', 'agent', ?, ?, ?)`,
			p.TaskID, workerID,
			fmt.Sprintf(`{"result":null,"task_status":"in_progress"}`),
			fmt.Sprintf(`{"result":"%s","task_status":"%s"}`, p.Result, taskStatus))

		return &EndSessionResult{
			SessionID:  sessionID,
			TaskID:     p.TaskID,
			Result:     p.Result,
			TaskStatus: taskStatus,
			EndedAt:    endedAt,
		}, nil
	})
}

// ListSessions lists sessions with optional filters.
func ListSessions(db *sql.DB, taskID, workerID string, activeOnly bool) ([]Session, error) {
	var where []string
	var args []interface{}

	if taskID != "" {
		where = append(where, "s.task_id = ?")
		args = append(args, taskID)
	}
	if workerID != "" {
		where = append(where, "s.worker_id = ?")
		args = append(args, workerID)
	}
	if activeOnly {
		where = append(where, "s.ended_at IS NULL")
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	query := fmt.Sprintf(`
		SELECT s.id, s.task_id, s.reservation_id, s.worker_id, s.agent_pid,
			s.worktree_path, s.branch, s.started_at, s.ended_at, s.result,
			s.notes, s.commits, s.files_changed
		FROM sessions s %s
		ORDER BY s.started_at DESC`, whereClause)

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var s Session
		var commitsJSON, filesJSON string
		if err := rows.Scan(&s.ID, &s.TaskID, &s.ReservationID, &s.WorkerID,
			&s.AgentPID, &s.WorktreePath, &s.Branch, &s.StartedAt,
			&s.EndedAt, &s.Result, &s.Notes, &commitsJSON, &filesJSON); err != nil {
			return nil, fmt.Errorf("scanning session: %w", err)
		}
		// Decode JSON arrays (invariant #2)
		json.Unmarshal([]byte(commitsJSON), &s.Commits)
		json.Unmarshal([]byte(filesJSON), &s.FilesChanged)
		if s.Commits == nil {
			s.Commits = []json.RawMessage{}
		}
		if s.FilesChanged == nil {
			s.FilesChanged = []string{}
		}
		sessions = append(sessions, s)
	}
	if sessions == nil {
		sessions = []Session{}
	}
	return sessions, nil
}

// RenewReservation extends the TTL of an active reservation.
func RenewReservation(db *sql.DB, taskID string) (*Reservation, error) {
	return WithTxImmediateResult(db, func(conn *sql.Conn) (*Reservation, error) {
		ctx := context.Background()

		var res Reservation
		var active int
		err := conn.QueryRowContext(ctx, `
			SELECT id, task_id, worker_id, active, reserved_at, expires_at, ttl_sec, machine_id
			FROM reservations WHERE task_id = ? AND active = 1`, taskID,
		).Scan(&res.ID, &res.TaskID, &res.WorkerID, &active, &res.ReservedAt,
			&res.ExpiresAt, &res.TTLSec, &res.MachineID)
		if err == sql.ErrNoRows {
			return nil, output.NewError(output.ErrNoActiveSession,
				fmt.Sprintf("no active reservation for task %s", taskID), false, "")
		}
		if err != nil {
			return nil, fmt.Errorf("querying reservation: %w", err)
		}

		_, err = conn.ExecContext(ctx, `
			UPDATE reservations SET
				expires_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now', '+' || ttl_sec || ' seconds')
			WHERE id = ? AND active = 1`, res.ID)
		if err != nil {
			return nil, fmt.Errorf("renewing reservation: %w", err)
		}

		conn.QueryRowContext(ctx, "SELECT expires_at FROM reservations WHERE id = ?", res.ID).Scan(&res.ExpiresAt)
		res.Active = true

		return &res, nil
	})
}

// LazyZombieScan performs non-blocking zombie detection per spec §6.3.
// Uses connection pinning to avoid deadlocks with SetMaxOpenConns(1).
func LazyZombieScan(database *sql.DB) {
	ctx := context.Background()
	conn, err := database.Conn(ctx)
	if err != nil {
		return
	}
	defer conn.Close()

	// Use busy_timeout = 0 to prevent CLI latency storms (invariant #4)
	if _, err := conn.ExecContext(ctx, "PRAGMA busy_timeout = 0"); err != nil {
		return
	}

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		// SQLITE_BUSY: another writer is active. Skip scan.
		conn.ExecContext(ctx, "PRAGMA busy_timeout = 5000") // restore
		return
	}

	// Find all sessions with no ended_at and a recorded PID
	rows, err := conn.QueryContext(ctx, `
		SELECT s.id, s.task_id, s.reservation_id, s.agent_pid, r.id as res_id, t.version
		FROM sessions s
		JOIN reservations r ON r.id = s.reservation_id
		JOIN tasks t ON t.id = s.task_id
		WHERE s.ended_at IS NULL AND s.agent_pid IS NOT NULL AND r.active = 1
	`)
	if err != nil {
		conn.ExecContext(ctx, "COMMIT")
		conn.ExecContext(ctx, "PRAGMA busy_timeout = 5000")
		return
	}

	type zombieSession struct {
		SessionID     string
		TaskID        string
		ReservationID string
		PID           int
		ResID         string
		TaskVersion   int
	}
	var toRecover []zombieSession
	for rows.Next() {
		var z zombieSession
		rows.Scan(&z.SessionID, &z.TaskID, &z.ReservationID, &z.PID, &z.ResID, &z.TaskVersion)
		if isProcessDead(z.PID) {
			toRecover = append(toRecover, z)
		}
	}
	rows.Close()

	for _, z := range toRecover {
		recoverZombieSessionConn(conn, ctx, z.SessionID, z.TaskID, z.ReservationID, z.PID, z.TaskVersion)
	}

	// Also expire reservations past expires_at
	conn.ExecContext(ctx, `
		UPDATE reservations SET active = 0,
			released_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
			release_reason = 'expired'
		WHERE active = 1 AND expires_at < strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
	`)

	// For expired reservations, also update task status and end sessions
	expiredRows, err := conn.QueryContext(ctx, `
		SELECT s.id, s.task_id, t.version
		FROM sessions s
		JOIN tasks t ON t.id = s.task_id
		JOIN reservations r ON r.id = s.reservation_id
		WHERE s.ended_at IS NULL AND r.active = 0 AND r.release_reason = 'expired'
	`)
	if err == nil {
		for expiredRows.Next() {
			var sid, tid string
			var tv int
			expiredRows.Scan(&sid, &tid, &tv)
			conn.ExecContext(ctx, `UPDATE sessions SET ended_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), result = 'interrupted' WHERE id = ?`, sid)
			conn.ExecContext(ctx, `UPDATE tasks SET status = 'ready', version = version + 1 WHERE id = ? AND version = ?`, tid, tv)
			conn.ExecContext(ctx, `INSERT INTO recovery_records (task_id, session_id, reason, previous_status) VALUES (?, ?, 'reservation_expired', 'in_progress')`, tid, sid)
		}
		expiredRows.Close()
	}

	conn.ExecContext(ctx, "COMMIT")

	// Restore busy_timeout for main CLI operation
	conn.ExecContext(ctx, "PRAGMA busy_timeout = 5000")
}

func recoverZombieSessionConn(conn *sql.Conn, ctx context.Context, sessionID, taskID, reservationID string, deadPID int, taskVersion int) {
	conn.ExecContext(ctx, `UPDATE sessions SET ended_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), result = 'interrupted' WHERE id = ? AND ended_at IS NULL`, sessionID)
	conn.ExecContext(ctx, `UPDATE reservations SET active = 0, released_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), release_reason = 'recovered' WHERE id = ? AND active = 1`, reservationID)
	conn.ExecContext(ctx, `UPDATE tasks SET status = 'ready', version = version + 1 WHERE id = ? AND version = ?`, taskID, taskVersion)
	conn.ExecContext(ctx, `INSERT INTO recovery_records (task_id, session_id, reason, previous_status, recovered_to) VALUES (?, ?, 'crash_detected', 'in_progress', 'ready')`, taskID, sessionID)
	conn.ExecContext(ctx, `INSERT INTO audit_events (task_id, event_type, actor_type, actor_id, new_value_json)
		VALUES (?, 'task.recovered', 'system', 'zombie_scan', ?)`, taskID,
		fmt.Sprintf(`{"dead_pid":%d,"session_id":"%s"}`, deadPID, sessionID))
}

// isProcessDead returns true if the PID does not exist on the OS per spec §6.3.
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

// GetActiveReservation gets the active reservation for a task.
func GetActiveReservation(db *sql.DB, taskID string) (*Reservation, error) {
	var res Reservation
	var active int
	err := db.QueryRow(`
		SELECT id, task_id, worker_id, active, reserved_at, expires_at, ttl_sec,
			released_at, release_reason, worktree_path, machine_id
		FROM reservations WHERE task_id = ? AND active = 1`, taskID,
	).Scan(&res.ID, &res.TaskID, &res.WorkerID, &active, &res.ReservedAt,
		&res.ExpiresAt, &res.TTLSec, &res.ReleasedAt, &res.ReleaseReason,
		&res.WorktreePath, &res.MachineID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	res.Active = active == 1
	return &res, nil
}
