package solo

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

func (a *App) CreateHandoff(taskID, summary, remaining, to string, files []string) (map[string]any, error) {
	if strings.TrimSpace(summary) == "" {
		return nil, ErrInvalidArgument("--summary is required")
	}
	return a.withDB(func(db *sql.DB) (map[string]any, error) {
		ctx := context.Background()
		handoffID := randomID()
		var sessionID, reservationID, worker string
		var sessionCommits string
		worktreeStatus := "clean"
		var version int
		if err := withImmediateTx(ctx, db, func(conn *sql.Conn) error {
			if err := conn.QueryRowContext(ctx, `SELECT s.id, s.reservation_id, s.worker_id, t.version
				FROM sessions s JOIN tasks t ON t.id=s.task_id
				WHERE s.task_id=? AND s.ended_at IS NULL`, taskID).Scan(&sessionID, &reservationID, &worker, &version); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return errNoActiveSession(taskID)
				}
				return err
			}
			res, err := conn.ExecContext(ctx, `UPDATE sessions SET ended_at=strftime('%Y-%m-%dT%H:%M:%fZ','now'), result='handoff' WHERE id=? AND ended_at IS NULL`, sessionID)
			if err != nil {
				return err
			}
			n, _ := res.RowsAffected()
			if n == 0 {
				return errNoActiveSession(taskID)
			}
			res, err = conn.ExecContext(ctx, `UPDATE reservations SET active=0, released_at=strftime('%Y-%m-%dT%H:%M:%fZ','now'), release_reason='handoff' WHERE id=? AND active=1`, reservationID)
			if err != nil {
				return err
			}
			n, _ = res.RowsAffected()
			if n == 0 {
				return errNoActiveSession(taskID)
			}
			_ = conn.QueryRowContext(ctx, `SELECT commits FROM sessions WHERE id=?`, sessionID).Scan(&sessionCommits)
			if strings.TrimSpace(sessionCommits) == "" {
				sessionCommits = "[]"
			}
			var worktreePath sql.NullString
			_ = conn.QueryRowContext(ctx, `SELECT worktree_path FROM reservations WHERE id=?`, reservationID).Scan(&worktreePath)
			if worktreePath.Valid {
				repoRoot, _ := discoverRepoRoot(".")
				if st, _, err := worktreeGitStatus(repoRoot, worktreePath.String); err == nil {
					worktreeStatus = st
				}
			}
			if _, err := conn.ExecContext(ctx, `INSERT INTO handoffs (id, task_id, session_id, reservation_id, from_worker, to_worker, summary, remaining_work, files_touched, commits_json, worktree_status)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, handoffID, taskID, sessionID, reservationID, worker, nullableString(to), summary, remaining, mustJSON(files), sessionCommits, worktreeStatus); err != nil {
				return err
			}
			res, err = conn.ExecContext(ctx, `UPDATE tasks SET status='ready', version=version+1, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=? AND version=?`, taskID, version)
			if err != nil {
				return err
			}
			n, _ = res.RowsAffected()
			if n == 0 {
				return errVersionConflict()
			}
			return writeAudit(ctx, conn, taskID, "handoff.created", "agent", worker, map[string]any{"status": "in_progress"}, map[string]any{"status": "ready", "handoff_id": handoffID})
		}); err != nil {
			return nil, err
		}
		return map[string]any{"handoff_id": handoffID, "task_id": taskID, "from_worker": worker, "to_worker": nullableStringOutput(to), "task_status": "ready", "session_ended": true, "reservation_released": true}, nil
	})
}

func nullableString(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

func nullableStringOutput(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

func (a *App) ListHandoffs(taskID, status string) (map[string]any, error) {
	return a.withDB(func(db *sql.DB) (map[string]any, error) {
		q := `SELECT id, task_id, from_worker, to_worker, summary, status, created_at FROM handoffs WHERE 1=1`
		args := []any{}
		if taskID != "" {
			q += ` AND task_id=?`
			args = append(args, taskID)
		}
		if status != "" {
			q += ` AND status=?`
			args = append(args, status)
		}
		q += ` ORDER BY created_at DESC`
		rows, err := db.Query(q, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		list := []map[string]any{}
		for rows.Next() {
			var id, tID, from, summary, st, created string
			var to sql.NullString
			if err := rows.Scan(&id, &tID, &from, &to, &summary, &st, &created); err != nil {
				return nil, err
			}
			var toVal any
			if to.Valid {
				toVal = to.String
			}
			list = append(list, map[string]any{"id": id, "task_id": tID, "from_worker": from, "to_worker": toVal, "summary": summary, "status": st, "created_at": created})
		}
		return map[string]any{"handoffs": list}, nil
	})
}

func (a *App) ShowHandoff(handoffID string) (map[string]any, error) {
	return a.withDB(func(db *sql.DB) (map[string]any, error) {
		row := db.QueryRow(`SELECT id, task_id, session_id, reservation_id, from_worker, to_worker, summary, remaining_work, files_touched, commits_json, error_context, worktree_status, status, created_at, accepted_at, expires_at
			FROM handoffs WHERE id=?`, handoffID)
		var id, tID, from, summary, rem, filesJSON, commitsJSON, status, created string
		var sess, resID, to, errCtx, wtStatus, accepted, expires sql.NullString
		if err := row.Scan(&id, &tID, &sess, &resID, &from, &to, &summary, &rem, &filesJSON, &commitsJSON, &errCtx, &wtStatus, &status, &created, &accepted, &expires); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, errWith("HANDOFF_NOT_FOUND", "Handoff not found: "+handoffID, false, "")
			}
			return nil, err
		}
		files, err := parseJSONList[string](filesJSON)
		if err != nil {
			return nil, err
		}
		commits, err := parseJSONList[map[string]any](commitsJSON)
		if err != nil {
			return nil, err
		}
		m := map[string]any{
			"id": id, "task_id": tID, "from_worker": from, "summary": summary, "remaining_work": rem,
			"files_touched": files, "commits_json": commits, "status": status, "created_at": created,
		}
		if sess.Valid {
			m["session_id"] = sess.String
		}
		if resID.Valid {
			m["reservation_id"] = resID.String
		}
		if to.Valid {
			m["to_worker"] = to.String
		} else {
			m["to_worker"] = nil
		}
		if errCtx.Valid {
			m["error_context"] = errCtx.String
		}
		if wtStatus.Valid {
			m["worktree_status"] = wtStatus.String
		}
		if accepted.Valid {
			m["accepted_at"] = accepted.String
		}
		if expires.Valid {
			m["expires_at"] = expires.String
		}
		return map[string]any{"handoff": m}, nil
	})
}
