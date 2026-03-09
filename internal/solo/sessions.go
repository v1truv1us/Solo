package solo

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (a *App) StartSession(taskID, worker string, ttl, pid int) (map[string]any, error) {
	if strings.TrimSpace(worker) == "" {
		return nil, ErrInvalidArgument("--worker is required")
	}
	return a.withDB(func(db *sql.DB) (map[string]any, error) {
		ctx := context.Background()
		if ttl <= 0 {
			ttl = configInt(db, "default_ttl_sec", 3600)
		}
		maxTTL := configInt(db, "max_ttl_sec", 86400)
		if ttl > maxTTL {
			ttl = maxTTL
		}
		machineID := configString(db, "machine_id", "default")
		worktreeDir := configString(db, "worktree_dir", ".solo/worktrees")
		baseRef := configString(db, "base_ref", "origin/main")
		maxWorktrees := configInt(db, "max_worktrees", 5)
		repoRoot, err := discoverRepoRoot(".")
		if err != nil {
			return nil, err
		}
		worktreePath := filepath.Join(repoRoot, worktreeDir, taskID)
		branch := "solo/" + machineID + "/" + taskID

		resID := randomID()
		sessionID := randomID()
		var taskVersion int
		var inProgressVersion int
		var expiresAt string

		if err := withImmediateTx(ctx, db, func(conn *sql.Conn) error {
			var activeSlots int
			if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM worktrees WHERE status IN ('active','cleanup_pending')`).Scan(&activeSlots); err != nil {
				return err
			}
			if activeSlots >= maxWorktrees {
				return errWorktreeLimitExceeded(maxWorktrees)
			}
			var currentStatus string
			if err := conn.QueryRowContext(ctx, `SELECT status, version FROM tasks WHERE id=?`, taskID).Scan(&currentStatus, &taskVersion); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return errTaskNotFound(taskID)
				}
				return err
			}
			if currentStatus != "ready" {
				return errTaskNotReady(currentStatus)
			}
			var lockedWorker sql.NullString
			_ = conn.QueryRowContext(ctx, `SELECT to_worker FROM handoffs WHERE task_id=? AND status='pending' ORDER BY created_at DESC LIMIT 1`, taskID).Scan(&lockedWorker)
			if lockedWorker.Valid && strings.TrimSpace(lockedWorker.String) != "" && lockedWorker.String != worker {
				return errHandoffLocked()
			}
			if _, err := conn.ExecContext(ctx, `INSERT INTO reservations (id, task_id, worker_id, expires_at, ttl_sec, machine_id)
				VALUES (?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now', ?), ?, ?)`, resID, taskID, worker, sqlTimeAddSeconds(ttl), ttl, machineID); err != nil {
				if strings.Contains(strings.ToLower(err.Error()), "unique") {
					return errTaskLocked(taskID)
				}
				return err
			}
			if err := conn.QueryRowContext(ctx, `SELECT expires_at FROM reservations WHERE id=?`, resID).Scan(&expiresAt); err != nil {
				return err
			}
			if _, err := conn.ExecContext(ctx, `INSERT INTO sessions (id, task_id, reservation_id, worker_id, agent_pid) VALUES (?, ?, ?, ?, ?)`, sessionID, taskID, resID, worker, pid); err != nil {
				return err
			}
			if _, err := conn.ExecContext(ctx, `INSERT INTO worktrees (path, task_id, branch_name, base_ref, base_commit_sha, status)
				VALUES (?, ?, ?, ?, NULL, 'cleanup_pending')`, worktreePath, taskID, branch, baseRef); err != nil {
				if strings.Contains(strings.ToLower(err.Error()), "unique") {
					return errWorktreeExists(worktreePath)
				}
				return err
			}
			res, err := conn.ExecContext(ctx, `UPDATE tasks SET status='in_progress', version=version+1, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=? AND version=?`, taskID, taskVersion)
			if err != nil {
				return err
			}
			n, _ := res.RowsAffected()
			if n == 0 {
				return errVersionConflict()
			}
			inProgressVersion = taskVersion + 1
			return writeAudit(ctx, conn, taskID, "session.start", "agent", worker, map[string]any{"status": "ready"}, map[string]any{"status": "in_progress", "session_id": sessionID})
		}); err != nil {
			return nil, err
		}

		if err := ensureRepoHasCommit(repoRoot); err != nil {
			_ = a.compensateStartFailure(db, taskID, sessionID, resID, worktreePath, inProgressVersion)
			return nil, err
		}
		if err := createWorktree(repoRoot, worktreePath, branch, baseRef); err != nil {
			_ = a.compensateStartFailure(db, taskID, sessionID, resID, worktreePath, inProgressVersion)
			return nil, err
		}
		if err := withImmediateTx(ctx, db, func(conn *sql.Conn) error {
			_, err := conn.ExecContext(ctx, `UPDATE worktrees SET status='active' WHERE path=? AND task_id=?`, worktreePath, taskID)
			if err != nil {
				return err
			}
			if _, err := conn.ExecContext(ctx, `UPDATE sessions SET worktree_path=?, branch=? WHERE id=?`, worktreePath, branch, sessionID); err != nil {
				return err
			}
			if _, err := conn.ExecContext(ctx, `UPDATE reservations SET worktree_path=? WHERE id=?`, worktreePath, resID); err != nil {
				return err
			}
			return nil
		}); err != nil {
			_ = removeWorktree(repoRoot, worktreePath, true)
			_ = deleteBranch(repoRoot, branch, true)
			_ = a.compensateStartFailure(db, taskID, sessionID, resID, worktreePath, inProgressVersion)
			return nil, err
		}

		ctxBundle, err := a.buildContextBundle(db, taskID, 0)
		if err != nil {
			return nil, err
		}
		relPath := relOrSelf(repoRoot, worktreePath)
		return map[string]any{
			"session_id":     sessionID,
			"reservation_id": resID,
			"worktree_path":  relPath,
			"branch":         branch,
			"expires_at":     expiresAt,
			"context":        ctxBundle,
		}, nil
	})
}

func (a *App) compensateStartFailure(db *sql.DB, taskID, sessionID, reservationID, worktreePath string, expectedVersion int) error {
	ctx := context.Background()
	return withImmediateTx(ctx, db, func(conn *sql.Conn) error {
		_, _ = conn.ExecContext(ctx, `DELETE FROM sessions WHERE id=?`, sessionID)
		_, _ = conn.ExecContext(ctx, `DELETE FROM reservations WHERE id=?`, reservationID)
		_, _ = conn.ExecContext(ctx, `DELETE FROM worktrees WHERE path=? AND status='cleanup_pending'`, worktreePath)
		_, _ = conn.ExecContext(ctx, `UPDATE tasks SET status='ready', version=version+1, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=? AND version=? AND status='in_progress'`, taskID, expectedVersion)
		return nil
	})
}

func (a *App) EndSession(taskID, result, notes string, commits, files []string, overrideStatus string) (map[string]any, error) {
	if result == "" {
		return nil, ErrInvalidArgument("--result is required")
	}
	if result != "completed" && result != "failed" && result != "interrupted" && result != "abandoned" {
		return nil, ErrInvalidArgument("--result must be one of completed|failed|interrupted|abandoned")
	}
	return a.withDB(func(db *sql.DB) (map[string]any, error) {
		ctx := context.Background()
		var sessionID string
		var reservationID string
		var currentVersion int
		taskStatus := "in_progress"
		endedAt := ""
		if err := withImmediateTx(ctx, db, func(conn *sql.Conn) error {
			if err := conn.QueryRowContext(ctx, `SELECT s.id, s.reservation_id, t.version FROM sessions s JOIN tasks t ON t.id=s.task_id WHERE s.task_id=? AND s.ended_at IS NULL`, taskID).Scan(&sessionID, &reservationID, &currentVersion); err != nil {
				if err == sql.ErrNoRows {
					return errNoActiveSession(taskID)
				}
				return err
			}
			res, err := conn.ExecContext(ctx, `UPDATE sessions SET ended_at=strftime('%Y-%m-%dT%H:%M:%fZ','now'), result=?, notes=?, commits=?, files_changed=? WHERE id=? AND ended_at IS NULL`, result, notes, mustJSON(decodeCommitShas(commits)), mustJSON(files), sessionID)
			if err != nil {
				return err
			}
			n, _ := res.RowsAffected()
			if n == 0 {
				return errNoActiveSession(taskID)
			}
			reason := map[string]string{"completed": "completed", "failed": "manual", "interrupted": "manual", "abandoned": "manual"}[result]
			if reason == "" {
				reason = "manual"
			}
			if _, err := conn.ExecContext(ctx, `UPDATE reservations SET active=0, released_at=strftime('%Y-%m-%dT%H:%M:%fZ','now'), release_reason=? WHERE id=? AND active=1`, reason, reservationID); err != nil {
				return err
			}
			target := "in_progress"
			switch result {
			case "completed":
				target = "in_review"
				if overrideStatus == "done" {
					target = "done"
				}
			case "interrupted", "abandoned":
				target = "ready"
			case "failed":
				target = "in_progress"
			}
			if target != "in_progress" {
				res, err = conn.ExecContext(ctx, `UPDATE tasks SET status=?, version=version+1, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=? AND version=?`, target, taskID, currentVersion)
				if err != nil {
					return err
				}
				n, _ = res.RowsAffected()
				if n == 0 {
					return errVersionConflict()
				}
				taskStatus = target
			}
			if err := conn.QueryRowContext(ctx, `SELECT ended_at FROM sessions WHERE id=?`, sessionID).Scan(&endedAt); err != nil {
				return err
			}
			return writeAudit(ctx, conn, taskID, "session.end", "agent", "cli", map[string]any{"status": "in_progress"}, map[string]any{"status": taskStatus, "result": result})
		}); err != nil {
			return nil, err
		}
		return map[string]any{"session_id": sessionID, "task_id": taskID, "result": result, "task_status": taskStatus, "ended_at": endedAt}, nil
	})
}

func (a *App) ListSessions(taskID, worker string, active bool) (map[string]any, error) {
	return a.withDB(func(db *sql.DB) (map[string]any, error) {
		query := `SELECT id, task_id, worker_id, started_at, ended_at, result FROM sessions WHERE 1=1`
		args := []any{}
		if taskID != "" {
			query += ` AND task_id=?`
			args = append(args, taskID)
		}
		if worker != "" {
			query += ` AND worker_id=?`
			args = append(args, worker)
		}
		if active {
			query += ` AND ended_at IS NULL`
		}
		query += ` ORDER BY started_at DESC`
		rows, err := db.Query(query, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		sessions := []map[string]any{}
		for rows.Next() {
			var id, tID, w, started string
			var ended, result sql.NullString
			if err := rows.Scan(&id, &tID, &w, &started, &ended, &result); err != nil {
				return nil, err
			}
			var endedVal any
			var resVal any
			if ended.Valid {
				endedVal = ended.String
			}
			if result.Valid {
				resVal = result.String
			}
			sessions = append(sessions, map[string]any{"id": id, "task_id": tID, "worker_id": w, "started_at": started, "ended_at": endedVal, "result": resVal})
		}
		return map[string]any{"sessions": sessions}, nil
	})
}

func (a *App) RenewReservation(taskID string) (map[string]any, error) {
	return a.withDB(func(db *sql.DB) (map[string]any, error) {
		ctx := context.Background()
		defaultTTL := configInt(db, "default_ttl_sec", 3600)
		var rid string
		var expires string
		if err := withImmediateTx(ctx, db, func(conn *sql.Conn) error {
			var ownerPID int
			if err := conn.QueryRowContext(ctx, `SELECT r.id, COALESCE(s.agent_pid, 0) FROM reservations r LEFT JOIN sessions s ON s.reservation_id=r.id AND s.ended_at IS NULL WHERE r.task_id=? AND r.active=1`, taskID).Scan(&rid, &ownerPID); err != nil {
				if err == sql.ErrNoRows {
					return errTaskLocked(taskID)
				}
				return err
			}
			// Assumption per spec ambiguity: "current holder" is identified by active session PID.
			if ownerPID > 0 && ownerPID != os.Getpid() {
				return errTaskLocked(taskID)
			}
			if _, err := conn.ExecContext(ctx, `UPDATE reservations SET expires_at=strftime('%Y-%m-%dT%H:%M:%fZ','now', ?) WHERE id=? AND active=1`, sqlTimeAddSeconds(defaultTTL), rid); err != nil {
				return err
			}
			if err := conn.QueryRowContext(ctx, `SELECT expires_at FROM reservations WHERE id=?`, rid).Scan(&expires); err != nil {
				return err
			}
			return nil
		}); err != nil {
			return nil, err
		}
		remaining := defaultTTL
		return map[string]any{"reservation": map[string]any{"id": rid, "task_id": taskID, "new_expires_at": expires, "remaining_sec": remaining}}, nil
	})
}

func parseTaskIDNumber(taskID string) int {
	v, _ := strconv.Atoi(strings.TrimPrefix(taskID, "T-"))
	return v
}

func relOrSelf(repoRoot, path string) string {
	rel, err := filepath.Rel(repoRoot, path)
	if err != nil {
		return path
	}
	return rel
}
