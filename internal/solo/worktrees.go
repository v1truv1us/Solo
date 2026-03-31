package solo

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
)

func (a *App) ListWorktrees() (map[string]any, error) {
	return a.withDB(func(db *sql.DB) (map[string]any, error) {
		repoRoot, _ := discoverRepoRoot(".")
		rows, err := db.Query(`SELECT path, task_id, branch_name, status, COALESCE(disk_usage_bytes, 0), created_at FROM worktrees ORDER BY created_at DESC`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		items := []map[string]any{}
		for rows.Next() {
			var path, taskID, branch, status, created string
			var bytes int64
			if err := rows.Scan(&path, &taskID, &branch, &status, &bytes, &created); err != nil {
				return nil, err
			}
			items = append(items, map[string]any{"path": relOrSelf(repoRoot, path), "task_id": taskID, "branch": branch, "status": status, "disk_usage_mb": bytes / (1024 * 1024), "created_at": created})
		}
		max := configInt(db, "max_worktrees", 5)
		return map[string]any{"worktrees": items, "total": len(items), "max": max}, nil
	})
}

func (a *App) InspectWorktree(taskID string) (map[string]any, error) {
	return a.withDB(func(db *sql.DB) (map[string]any, error) {
		var path, tID, branch, status, baseRef string
		var bytes int64
		if err := db.QueryRow(`SELECT path, task_id, branch_name, status, base_ref, COALESCE(disk_usage_bytes, 0) FROM worktrees WHERE task_id=? ORDER BY created_at DESC LIMIT 1`, taskID).
			Scan(&path, &tID, &branch, &status, &baseRef, &bytes); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, errWith("WORKTREE_NOT_FOUND", "No worktree for task: "+taskID, false, "")
			}
			return nil, err
		}
		repoRoot, err := discoverRepoRoot(".")
		if err != nil {
			return nil, err
		}
		gStatus, files, _ := worktreeGitStatus(repoRoot, path)
		ahead, behind := commitCountAheadBehind(repoRoot, path, baseRef)
		relPath := relOrSelf(repoRoot, path)
		return map[string]any{
			"path": relPath, "task_id": tID, "branch": branch, "status": status,
			"git_status": gStatus, "uncommitted_files": files,
			"ahead_commits": ahead, "behind_commits": behind, "disk_usage_mb": bytes / (1024 * 1024),
		}, nil
	})
}

func (a *App) CleanupWorktrees(taskID string, force bool) (map[string]any, error) {
	return a.withDB(func(db *sql.DB) (map[string]any, error) {
		repoRoot, err := discoverRepoRoot(".")
		if err != nil {
			return nil, err
		}
		query := `SELECT path, task_id, branch_name FROM worktrees WHERE 1=1`
		args := []any{}
		if taskID != "" {
			query += ` AND task_id=?`
			args = append(args, taskID)
		}
		rows, err := db.Query(query, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		cleaned := []map[string]any{}
		skipped := []map[string]any{}
		for rows.Next() {
			var path, tID, branch string
			if err := rows.Scan(&path, &tID, &branch); err != nil {
				return nil, err
			}
			var activeCount int
			if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM reservations WHERE task_id=? AND active=1) + (SELECT COUNT(*) FROM sessions WHERE task_id=? AND ended_at IS NULL)`, tID, tID).Scan(&activeCount); err != nil {
				return nil, err
			}
			if activeCount > 0 {
				skipped = append(skipped, map[string]any{"task_id": tID, "path": path, "reason": "active"})
				continue
			}
			status, files, _ := worktreeGitStatus(repoRoot, path)
			if status != "clean" && !force {
				skipped = append(skipped, map[string]any{"task_id": tID, "path": path, "reason": "dirty", "uncommitted_files": files})
				continue
			}
			if err := removeWorktree(repoRoot, path, force); err != nil {
				skipped = append(skipped, map[string]any{"task_id": tID, "path": path, "reason": err.Error()})
				continue
			}
			_ = deleteBranch(repoRoot, branch, force)
			ctx := context.Background()
			if err := withImmediateTx(ctx, db, func(conn *sql.Conn) error {
				_, err := conn.ExecContext(ctx, `DELETE FROM worktrees WHERE path=? AND status IN ('active','cleanup_pending')`, path)
				return err
			}); err != nil {
				skipped = append(skipped, map[string]any{"task_id": tID, "path": path, "reason": err.Error()})
				continue
			}
			cleaned = append(cleaned, map[string]any{"task_id": tID, "path": path})
		}
		return map[string]any{"cleaned": cleaned, "skipped": skipped}, nil
	})
}

func dirSize(path string) int64 {
	var total int64
	_ = filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}
