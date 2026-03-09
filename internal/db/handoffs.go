package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/v1truv1us/solo/internal/output"
)

// Handoff represents a handoff record.
type Handoff struct {
	ID            string            `json:"id"`
	TaskID        string            `json:"task_id"`
	SessionID     *string           `json:"session_id,omitempty"`
	ReservationID *string           `json:"reservation_id,omitempty"`
	FromWorker    string            `json:"from_worker"`
	ToWorker      *string           `json:"to_worker"`
	Summary       string            `json:"summary"`
	RemainingWork string            `json:"remaining_work"`
	FilesTouched  []string          `json:"files_touched"`
	CommitsJSON   []json.RawMessage `json:"commits_json"`
	ErrorContext  *string           `json:"error_context,omitempty"`
	WorktreeStatus *string          `json:"worktree_status,omitempty"`
	Status        string            `json:"status"`
	CreatedAt     string            `json:"created_at"`
	AcceptedAt    *string           `json:"accepted_at,omitempty"`
	ExpiresAt     *string           `json:"expires_at,omitempty"`
}

// CreateHandoffParams holds parameters for creating a handoff.
type CreateHandoffParams struct {
	TaskID         string
	Summary        string
	RemainingWork  string
	ToWorker       string
	Files          []string
	WorktreeStatus string
}

// CreateHandoffResult holds the result of creating a handoff.
type CreateHandoffResult struct {
	HandoffID           string  `json:"handoff_id"`
	TaskID              string  `json:"task_id"`
	FromWorker          string  `json:"from_worker"`
	ToWorker            *string `json:"to_worker"`
	TaskStatus          string  `json:"task_status"`
	SessionEnded        bool    `json:"session_ended"`
	ReservationReleased bool    `json:"reservation_released"`
}

// CreateHandoff performs the atomic handoff per spec §6.4.
func CreateHandoff(db *sql.DB, p CreateHandoffParams) (*CreateHandoffResult, error) {
	if strings.TrimSpace(p.Summary) == "" {
		return nil, output.NewError(output.ErrInvalidArgument, "summary is required", false, "")
	}

	return WithTxImmediateResult(db, func(conn *sql.Conn) (*CreateHandoffResult, error) {
		ctx := context.Background()

		// Find active session
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

		// End the session
		conn.ExecContext(ctx, `UPDATE sessions SET ended_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), result = 'handoff'
			WHERE id = ? AND ended_at IS NULL`, sessionID)

		// Release the reservation
		conn.ExecContext(ctx, `UPDATE reservations SET active = 0,
			released_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), release_reason = 'handoff'
			WHERE id = ? AND active = 1`, reservationID)

		// Create handoff record
		handoffID := uuid.New().String()
		filesJSON, _ := json.Marshal(p.Files)
		if p.Files == nil {
			filesJSON = []byte("[]")
		}

		var toWorker *string
		if p.ToWorker != "" {
			toWorker = &p.ToWorker
		}

		var wtStatus *string
		if p.WorktreeStatus != "" {
			wtStatus = &p.WorktreeStatus
		}

		_, err = conn.ExecContext(ctx, `
			INSERT INTO handoffs (id, task_id, session_id, reservation_id, from_worker, to_worker,
				summary, remaining_work, files_touched, worktree_status)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			handoffID, p.TaskID, sessionID, reservationID, workerID, toWorker,
			p.Summary, p.RemainingWork, string(filesJSON), wtStatus)
		if err != nil {
			return nil, fmt.Errorf("inserting handoff: %w", err)
		}

		// Transition task back to ready with OCC check
		result, err := conn.ExecContext(ctx, `
			UPDATE tasks SET status = 'ready', version = version + 1,
				updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
			WHERE id = ? AND version = ?`, p.TaskID, taskVersion)
		if err != nil {
			return nil, fmt.Errorf("updating task: %w", err)
		}
		rows, _ := result.RowsAffected()
		if rows == 0 {
			return nil, output.NewError(output.ErrVersionConflict,
				"task version changed during handoff", true, "Re-read current version, retry")
		}

		// Audit event
		conn.ExecContext(ctx, `INSERT INTO audit_events (task_id, event_type, actor_type, actor_id, new_value_json)
			VALUES (?, 'handoff.created', 'agent', ?, ?)`,
			p.TaskID, workerID,
			fmt.Sprintf(`{"handoff_id":"%s","to_worker":"%s"}`, handoffID, p.ToWorker))

		return &CreateHandoffResult{
			HandoffID:           handoffID,
			TaskID:              p.TaskID,
			FromWorker:          workerID,
			ToWorker:            toWorker,
			TaskStatus:          "ready",
			SessionEnded:        true,
			ReservationReleased: true,
		}, nil
	})
}

// ListHandoffs lists handoffs with optional filters.
func ListHandoffs(db *sql.DB, taskID, status string) ([]Handoff, error) {
	var where []string
	var args []interface{}

	if taskID != "" {
		where = append(where, "task_id = ?")
		args = append(args, taskID)
	}
	if status != "" {
		where = append(where, "status = ?")
		args = append(args, status)
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	query := fmt.Sprintf(`
		SELECT id, task_id, session_id, reservation_id, from_worker, to_worker,
			summary, remaining_work, files_touched, commits_json, error_context,
			worktree_status, status, created_at, accepted_at, expires_at
		FROM handoffs %s ORDER BY created_at DESC`, whereClause)

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing handoffs: %w", err)
	}
	defer rows.Close()

	var handoffs []Handoff
	for rows.Next() {
		var h Handoff
		var filesJSON, commitsJSON string
		if err := rows.Scan(&h.ID, &h.TaskID, &h.SessionID, &h.ReservationID,
			&h.FromWorker, &h.ToWorker, &h.Summary, &h.RemainingWork,
			&filesJSON, &commitsJSON, &h.ErrorContext, &h.WorktreeStatus,
			&h.Status, &h.CreatedAt, &h.AcceptedAt, &h.ExpiresAt); err != nil {
			return nil, fmt.Errorf("scanning handoff: %w", err)
		}
		json.Unmarshal([]byte(filesJSON), &h.FilesTouched)
		json.Unmarshal([]byte(commitsJSON), &h.CommitsJSON)
		if h.FilesTouched == nil {
			h.FilesTouched = []string{}
		}
		if h.CommitsJSON == nil {
			h.CommitsJSON = []json.RawMessage{}
		}
		handoffs = append(handoffs, h)
	}
	if handoffs == nil {
		handoffs = []Handoff{}
	}
	return handoffs, nil
}

// GetHandoff retrieves a single handoff by ID.
func GetHandoff(db *sql.DB, handoffID string) (*Handoff, error) {
	var h Handoff
	var filesJSON, commitsJSON string
	err := db.QueryRow(`
		SELECT id, task_id, session_id, reservation_id, from_worker, to_worker,
			summary, remaining_work, files_touched, commits_json, error_context,
			worktree_status, status, created_at, accepted_at, expires_at
		FROM handoffs WHERE id = ?`, handoffID,
	).Scan(&h.ID, &h.TaskID, &h.SessionID, &h.ReservationID,
		&h.FromWorker, &h.ToWorker, &h.Summary, &h.RemainingWork,
		&filesJSON, &commitsJSON, &h.ErrorContext, &h.WorktreeStatus,
		&h.Status, &h.CreatedAt, &h.AcceptedAt, &h.ExpiresAt)
	if err == sql.ErrNoRows {
		return nil, output.NewError(output.ErrTaskNotFound,
			fmt.Sprintf("handoff %q not found", handoffID), false, "")
	}
	if err != nil {
		return nil, fmt.Errorf("querying handoff: %w", err)
	}
	json.Unmarshal([]byte(filesJSON), &h.FilesTouched)
	json.Unmarshal([]byte(commitsJSON), &h.CommitsJSON)
	if h.FilesTouched == nil {
		h.FilesTouched = []string{}
	}
	if h.CommitsJSON == nil {
		h.CommitsJSON = []json.RawMessage{}
	}
	return &h, nil
}
