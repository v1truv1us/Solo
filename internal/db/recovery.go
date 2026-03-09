package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/v1truv1us/solo/internal/output"
)

// RecoveryRecord represents a recovery record.
type RecoveryRecord struct {
	ID               int      `json:"id"`
	TaskID           string   `json:"task_id"`
	SessionID        *string  `json:"session_id,omitempty"`
	Reason           string   `json:"reason"`
	PreviousStatus   string   `json:"previous_status"`
	RecoveredTo      string   `json:"recovered_to"`
	WorktreeState    *string  `json:"worktree_state,omitempty"`
	UncommittedFiles []string `json:"uncommitted_files"`
	DiagnosticJSON   *string  `json:"diagnostic_json,omitempty"`
	CreatedAt        string   `json:"created_at"`
}

// RecoverTaskResult holds the result of recovering a task.
type RecoverTaskResult struct {
	Recovered           bool    `json:"recovered"`
	TaskID              string  `json:"task_id"`
	PreviousStatus      string  `json:"previous_status"`
	RecoveredTo         string  `json:"recovered_to"`
	SessionEnded        *string `json:"session_ended,omitempty"`
	ReservationReleased *string `json:"reservation_released,omitempty"`
	WorktreeState       *string `json:"worktree_state,omitempty"`
	RecoveryRecordID    int     `json:"recovery_record_id"`
}

// RecoverTask performs manual recovery per spec §6.6.
func RecoverTask(db *sql.DB, taskID string, version int, worktreeState *string, uncommittedFiles []string) (*RecoverTaskResult, error) {
	return WithTxImmediateResult(db, func(conn *sql.Conn) (*RecoverTaskResult, error) {
		ctx := context.Background()

		// Get current task status
		var currentStatus string
		var currentVersion int
		err := conn.QueryRowContext(ctx, "SELECT status, version FROM tasks WHERE id = ?", taskID).Scan(&currentStatus, &currentVersion)
		if err == sql.ErrNoRows {
			return nil, output.NewError(output.ErrTaskNotFound,
				fmt.Sprintf("task %q not found", taskID), false, "")
		}
		if err != nil {
			return nil, fmt.Errorf("querying task: %w", err)
		}

		// OCC check (invariant #5)
		if version != currentVersion {
			return nil, output.NewError(output.ErrVersionConflict,
				fmt.Sprintf("version mismatch: expected %d, got %d", currentVersion, version),
				true, "Re-read current version, retry")
		}

		result := &RecoverTaskResult{
			TaskID:         taskID,
			PreviousStatus: currentStatus,
			RecoveredTo:    "ready",
		}

		// Find open session (if any)
		var sessionID, reservationID sql.NullString
		var agentPID sql.NullInt64
		err = conn.QueryRowContext(ctx, `
			SELECT id, reservation_id, agent_pid
			FROM sessions WHERE task_id = ? AND ended_at IS NULL`, taskID,
		).Scan(&sessionID, &reservationID, &agentPID)
		if err != nil && err != sql.ErrNoRows {
			return nil, fmt.Errorf("querying session: %w", err)
		}

		// End the session
		if sessionID.Valid {
			conn.ExecContext(ctx, `UPDATE sessions SET ended_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), result = 'interrupted'
				WHERE task_id = ? AND ended_at IS NULL`, taskID)
			s := sessionID.String
			result.SessionEnded = &s
		}

		// Release the reservation
		conn.ExecContext(ctx, `UPDATE reservations SET active = 0,
			released_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), release_reason = 'recovered'
			WHERE task_id = ? AND active = 1`, taskID)
		if reservationID.Valid {
			r := reservationID.String
			result.ReservationReleased = &r
		}

		// Reset task status with OCC check
		res, err := conn.ExecContext(ctx, `UPDATE tasks SET status = 'ready', version = version + 1,
			updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
			WHERE id = ? AND version = ?`, taskID, version)
		if err != nil {
			return nil, fmt.Errorf("updating task: %w", err)
		}
		rows, _ := res.RowsAffected()
		if rows == 0 {
			return nil, output.NewError(output.ErrVersionConflict,
				"version changed during recovery", true, "Re-read current version, retry")
		}

		// Create recovery record
		uncommittedJSON, _ := json.Marshal(uncommittedFiles)
		if uncommittedFiles == nil {
			uncommittedJSON = []byte("[]")
		}

		var sid *string
		if sessionID.Valid {
			s := sessionID.String
			sid = &s
		}

		recResult, err := conn.ExecContext(ctx, `
			INSERT INTO recovery_records (task_id, session_id, reason, previous_status, recovered_to,
				worktree_state, uncommitted_files)
			VALUES (?, ?, 'manual_recovery', ?, 'ready', ?, ?)`,
			taskID, sid, currentStatus, worktreeState, string(uncommittedJSON))
		if err != nil {
			return nil, fmt.Errorf("inserting recovery record: %w", err)
		}
		recID, _ := recResult.LastInsertId()
		result.RecoveryRecordID = int(recID)
		result.Recovered = true
		result.WorktreeState = worktreeState

		// Audit
		conn.ExecContext(ctx, `INSERT INTO audit_events (task_id, event_type, actor_type, actor_id, old_value_json, new_value_json)
			VALUES (?, 'task.recovered', 'system', 'cli', ?, ?)`,
			taskID,
			fmt.Sprintf(`{"status":"%s","version":%d}`, currentStatus, version),
			fmt.Sprintf(`{"status":"ready","version":%d}`, version+1))

		return result, nil
	})
}

// RecoverAllResult holds the result of recovering all zombie sessions.
type RecoverAllResult struct {
	Scanned   int                      `json:"scanned"`
	Recovered int                      `json:"recovered"`
	Records   []map[string]interface{} `json:"records"`
}

// RecoverAll performs manual recovery on all active sessions with dead PIDs.
func RecoverAll(db *sql.DB) (*RecoverAllResult, error) {
	return WithTxImmediateResult(db, func(conn *sql.Conn) (*RecoverAllResult, error) {
		ctx := context.Background()

		rows, err := conn.QueryContext(ctx, `
			SELECT s.id, s.task_id, s.reservation_id, s.agent_pid, t.version
			FROM sessions s
			JOIN reservations r ON r.id = s.reservation_id
			JOIN tasks t ON t.id = s.task_id
			WHERE s.ended_at IS NULL AND r.active = 1
		`)
		if err != nil {
			return nil, fmt.Errorf("querying sessions: %w", err)
		}

		type zombieInfo struct {
			SessionID     string
			TaskID        string
			ReservationID string
			PID           int
			TaskVersion   int
		}
		var zombies []zombieInfo
		var scanned int
		for rows.Next() {
			var z zombieInfo
			rows.Scan(&z.SessionID, &z.TaskID, &z.ReservationID, &z.PID, &z.TaskVersion)
			scanned++
			if z.PID > 0 && isProcessDead(z.PID) {
				zombies = append(zombies, z)
			}
		}
		rows.Close()

		// Also check expired reservations
		expiredRows, _ := conn.QueryContext(ctx, `
			SELECT s.id, s.task_id, s.reservation_id, s.agent_pid, t.version
			FROM sessions s
			JOIN reservations r ON r.id = s.reservation_id
			JOIN tasks t ON t.id = s.task_id
			WHERE s.ended_at IS NULL AND r.active = 1 AND r.expires_at < strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		`)
		if expiredRows != nil {
			for expiredRows.Next() {
				var z zombieInfo
				expiredRows.Scan(&z.SessionID, &z.TaskID, &z.ReservationID, &z.PID, &z.TaskVersion)
				// Check not already in zombies list
				found := false
				for _, existing := range zombies {
					if existing.SessionID == z.SessionID {
						found = true
						break
					}
				}
				if !found {
					zombies = append(zombies, z)
				}
			}
			expiredRows.Close()
		}

		var records []map[string]interface{}
		for _, z := range zombies {
			reason := "crash_detected"
			if z.PID <= 0 || !isProcessDead(z.PID) {
				reason = "reservation_expired"
			}

			conn.ExecContext(ctx, `UPDATE sessions SET ended_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), result = 'interrupted' WHERE id = ? AND ended_at IS NULL`, z.SessionID)
			conn.ExecContext(ctx, `UPDATE reservations SET active = 0, released_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), release_reason = 'recovered' WHERE id = ? AND active = 1`, z.ReservationID)
			conn.ExecContext(ctx, `UPDATE tasks SET status = 'ready', version = version + 1 WHERE id = ? AND version = ?`, z.TaskID, z.TaskVersion)
			conn.ExecContext(ctx, `INSERT INTO recovery_records (task_id, session_id, reason, previous_status) VALUES (?, ?, ?, 'in_progress')`, z.TaskID, z.SessionID, reason)

			records = append(records, map[string]interface{}{
				"task_id":    z.TaskID,
				"session_id": z.SessionID,
				"dead_pid":   z.PID,
				"reason":     reason,
			})
		}
		if records == nil {
			records = []map[string]interface{}{}
		}

		return &RecoverAllResult{
			Scanned:   scanned,
			Recovered: len(zombies),
			Records:   records,
		}, nil
	})
}
