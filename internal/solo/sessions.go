package solo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
)

func validateTaskID(id string) error {
	for _, r := range id {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return ErrInvalidArgument("task id contains invalid characters")
		}
	}
	return nil
}

func (a *App) StartSession(taskID, worker string, ttl, pid int) (map[string]any, error) {
	const maxAgentPID = 4194304
	if pid < 0 || pid > maxAgentPID {
		return nil, ErrInvalidArgument("--pid must be between 0 and 4194304")
	}
	if strings.TrimSpace(worker) == "" {
		return nil, ErrInvalidArgument("--worker is required")
	}
	if err := validateTaskID(taskID); err != nil {
		return nil, err
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
		cleanWorktree, err := filepath.Rel(repoRoot, worktreePath)
		if err != nil || strings.HasPrefix(cleanWorktree, "..") {
			return nil, ErrInvalidArgument("worktree path escapes repository root")
		}
		branch := "solo/" + machineID + "/" + taskID

		resID := randomID()
		sessionID := randomID()
		reservationToken := randomID()
		startCommitSHA := getRefSHA(repoRoot, "HEAD")
		var taskVersion int
		var inProgressVersion int
		var expiresAt string
		activeStored := taskStatusForWrite(db, "active")

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
			if _, err := conn.ExecContext(ctx, `INSERT INTO reservations (id, task_id, worker_id, expires_at, ttl_sec, machine_id, token)
				VALUES (?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now', ?), ?, ?, ?)`, resID, taskID, worker, sqlTimeAddSeconds(ttl), ttl, machineID, reservationToken); err != nil {
				if strings.Contains(strings.ToLower(err.Error()), "unique") {
					return errTaskLocked(taskID)
				}
				return err
			}
			if err := conn.QueryRowContext(ctx, `SELECT expires_at FROM reservations WHERE id=?`, resID).Scan(&expiresAt); err != nil {
				return err
			}
				var pidVal any
			if pid > 0 {
				pidVal = pid
			}
			if _, err := conn.ExecContext(ctx, `INSERT INTO sessions (id, task_id, reservation_id, worker_id, agent_pid, start_commit_sha) VALUES (?, ?, ?, ?, ?, ?)`, sessionID, taskID, resID, worker, pidVal, startCommitSHA); err != nil {
				return err
			}
			baseCommitSHA := getRefSHA(repoRoot, baseRef)
			var baseCommitSHAVal any
			if baseCommitSHA != "" {
				baseCommitSHAVal = baseCommitSHA
			}
			if _, err := conn.ExecContext(ctx, `INSERT INTO worktrees (path, task_id, branch_name, base_ref, base_commit_sha, status)
				VALUES (?, ?, ?, ?, ?, 'cleanup_pending')`, worktreePath, taskID, branch, baseRef, baseCommitSHAVal); err != nil {
				if strings.Contains(strings.ToLower(err.Error()), "unique") {
					return errWorktreeExists(worktreePath)
				}
				return err
			}
			res, err := conn.ExecContext(ctx, `UPDATE tasks SET status=?, version=version+1, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=? AND version=?`, activeStored, taskID, taskVersion)
			if err != nil {
				return err
			}
			n, _ := res.RowsAffected()
			if n == 0 {
				return errVersionConflict()
			}
			inProgressVersion = taskVersion + 1
			return writeAudit(ctx, conn, taskID, "session.start", "agent", worker, map[string]any{"status": "ready"}, map[string]any{"status": "active", "session_id": sessionID})
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
			"session_id":        sessionID,
			"reservation_id":    resID,
			"reservation_token": reservationToken,
			"worktree_path":     relPath,
			"branch":            branch,
			"expires_at":        expiresAt,
			"context":           ctxBundle,
		}, nil
	})
}

func (a *App) compensateStartFailure(db *sql.DB, taskID, sessionID, reservationID, worktreePath string, expectedVersion int) error {
	ctx := context.Background()
	activeStored := taskStatusForWrite(db, "active")
	return withImmediateTx(ctx, db, func(conn *sql.Conn) error {
		_, _ = conn.ExecContext(ctx, `DELETE FROM sessions WHERE id=?`, sessionID)
		_, _ = conn.ExecContext(ctx, `DELETE FROM reservations WHERE id=?`, reservationID)
		_, _ = conn.ExecContext(ctx, `DELETE FROM worktrees WHERE path=? AND status='cleanup_pending'`, worktreePath)
		_, _ = conn.ExecContext(ctx, `UPDATE tasks SET status='ready', version=version+1, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=? AND version=? AND status=?`, taskID, expectedVersion, activeStored)
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
		taskStatus := "active"
		endedAt := ""
		// Note: discoverRepoRoot failure is tolerated — end_commit_sha will be empty,
		// which correctly represents "commit SHA unavailable outside a repo context".
		repoRoot, _ := discoverRepoRoot(".")
		endCommitSHA := getRefSHA(repoRoot, "HEAD")
		if err := withImmediateTx(ctx, db, func(conn *sql.Conn) error {
			if err := conn.QueryRowContext(ctx, `SELECT s.id, s.reservation_id, t.version FROM sessions s JOIN tasks t ON t.id=s.task_id WHERE s.task_id=? AND s.ended_at IS NULL`, taskID).Scan(&sessionID, &reservationID, &currentVersion); err != nil {
				if err == sql.ErrNoRows {
					return errNoActiveSession(taskID)
				}
				return err
			}
			res, err := conn.ExecContext(ctx, `UPDATE sessions SET ended_at=strftime('%Y-%m-%dT%H:%M:%fZ','now'), result=?, notes=?, commits=?, files_changed=?, end_commit_sha=? WHERE id=? AND ended_at IS NULL`, result, notes, mustJSON(decodeCommitShas(commits)), mustJSON(files), endCommitSHA, sessionID)
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
			target := "active"
			switch result {
			case "completed":
				target = "completed"
				if overrideStatus != "" {
					if normalized, ok := normalizeTaskStatus(overrideStatus); ok {
						target = normalized
					}
				}
			case "interrupted", "abandoned":
				target = "ready"
			case "failed":
				target = "failed"
			}
			if target != "active" {
				storedTarget := taskStatusForWrite(db, target)
				res, err = conn.ExecContext(ctx, `UPDATE tasks SET status=?, version=version+1, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=? AND version=?`, storedTarget, taskID, currentVersion)
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
			return writeAudit(ctx, conn, taskID, "session.end", "agent", "cli", map[string]any{"status": "active"}, map[string]any{"status": taskStatus, "result": result})
		}); err != nil {
			return nil, err
		}

		// Aggregate files_changed from all sessions into the task's affected_files
		aggregateFilesToTask(db, taskID, files)

		return map[string]any{"session_id": sessionID, "task_id": taskID, "result": result, "task_status": taskStatus, "ended_at": endedAt, "end_commit_sha": endCommitSHA}, nil
	})
}

// aggregateFilesToTask merges new files from the current session into the task's
// affected_files, deduplicating across all sessions.
//
// This is intentionally best-effort and runs outside a transaction:
// it reads the task's current affected_files, merges in files from all completed
// sessions plus the current session's new files, and writes back. Under concurrent
// access the last writer wins, which is acceptable because file lists are append-only
// and deduplicated. Errors are silently ignored to avoid failing the session-end flow.
func aggregateFilesToTask(db *sql.DB, taskID string, newFiles []string) {
	var existingJSON string
	if err := db.QueryRow(`SELECT affected_files FROM tasks WHERE id=?`, taskID).Scan(&existingJSON); err != nil {
		return
	}
	var existing []string
	if err := json.Unmarshal([]byte(existingJSON), &existing); err != nil {
		existing = nil
	}
	seen := map[string]bool{}
	for _, f := range existing {
		seen[f] = true
	}
	for _, f := range newFiles {
		if !seen[f] {
			existing = append(existing, f)
			seen[f] = true
		}
	}
	// Also pull files from all completed sessions
	rows, err := db.Query(`SELECT files_changed FROM sessions WHERE task_id=? AND ended_at IS NOT NULL`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var sessionFilesJSON string
			if err := rows.Scan(&sessionFilesJSON); err != nil {
				continue
			}
			var sessionFiles []string
			if err := json.Unmarshal([]byte(sessionFilesJSON), &sessionFiles); err != nil {
				continue
			}
			for _, f := range sessionFiles {
				if !seen[f] {
					existing = append(existing, f)
					seen[f] = true
				}
			}
		}
	}
	merged, err := json.Marshal(existing)
	if err != nil {
		return
	}
	_, _ = db.Exec(`UPDATE tasks SET affected_files=? WHERE id=?`, string(merged), taskID)
}

func (a *App) ListSessions(taskID, worker string, active, verbose bool) (map[string]any, error) {
	return a.withDB(func(db *sql.DB) (map[string]any, error) {
		query := `SELECT id, task_id, worker_id, started_at, ended_at, result`
		if verbose {
			query += `, start_commit_sha, end_commit_sha, commits, files_changed`
		}
		query += ` FROM sessions WHERE 1=1`
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
			session := map[string]any{}
			if !verbose {
				if err := rows.Scan(&id, &tID, &w, &started, &ended, &result); err != nil {
					return nil, err
				}
			} else {
				var startSHA, endSHA, commitsJSON, filesJSON sql.NullString
				if err := rows.Scan(&id, &tID, &w, &started, &ended, &result, &startSHA, &endSHA, &commitsJSON, &filesJSON); err != nil {
					return nil, err
				}
				if startSHA.Valid && startSHA.String != "" {
					session["start_commit_sha"] = startSHA.String
				}
				if endSHA.Valid && endSHA.String != "" {
					session["end_commit_sha"] = endSHA.String
				}
				if commitsJSON.Valid && commitsJSON.String != "" {
					var c []any
					if err := json.Unmarshal([]byte(commitsJSON.String), &c); err == nil {
						session["commits"] = c
					}
				}
				if filesJSON.Valid && filesJSON.String != "" {
					var f []string
					if err := json.Unmarshal([]byte(filesJSON.String), &f); err == nil {
						session["files_changed"] = f
					}
				}
			}
			var endedVal any
			var resVal any
			if ended.Valid {
				endedVal = ended.String
			}
			if result.Valid {
				resVal = result.String
			}
			session["id"] = id
			session["task_id"] = tID
			session["worker_id"] = w
			session["started_at"] = started
			session["ended_at"] = endedVal
			session["result"] = resVal
			sessions = append(sessions, session)
		}
		return map[string]any{"sessions": sessions}, nil
	})
}

func relOrSelf(repoRoot, path string) string {
	rel, err := filepath.Rel(repoRoot, path)
	if err != nil {
		return path
	}
	return rel
}

func (a *App) RenewReservation(taskID string, token string) (map[string]any, error) {
	return a.withDB(func(db *sql.DB) (map[string]any, error) {
		ctx := context.Background()
		defaultTTL := configInt(db, "default_ttl_sec", 3600)
		var rid string
		var expires string
		if err := withImmediateTx(ctx, db, func(conn *sql.Conn) error {
			var storedToken sql.NullString
			if err := conn.QueryRowContext(ctx, `SELECT r.id, r.token FROM reservations r WHERE r.task_id=? AND r.active=1`, taskID).Scan(&rid, &storedToken); err != nil {
				if err == sql.ErrNoRows {
					return errTaskLocked(taskID)
				}
				return err
			}
			if storedToken.Valid && storedToken.String != "" && token != "" && storedToken.String != token {
				_ = writeAudit(ctx, conn, taskID, "reservation.renew_denied", "agent", "cli", nil, map[string]any{"reason": "token_mismatch"})
				return errTaskLocked(taskID)
			}
			if _, err := conn.ExecContext(ctx, `UPDATE reservations SET expires_at=strftime('%Y-%m-%dT%H:%M:%fZ','now', ?) WHERE id=? AND active=1`, sqlTimeAddSeconds(defaultTTL), rid); err != nil {
				return err
			}
			if err := conn.QueryRowContext(ctx, `SELECT expires_at FROM reservations WHERE id=?`, rid).Scan(&expires); err != nil {
				return err
			}
			return writeAudit(ctx, conn, taskID, "reservation.renewed", "agent", "cli", nil, map[string]any{"reservation_id": rid})
		}); err != nil {
			return nil, err
		}
		remaining := defaultTTL
		return map[string]any{"reservation": map[string]any{"id": rid, "task_id": taskID, "new_expires_at": expires, "remaining_sec": remaining}}, nil
	})
}
