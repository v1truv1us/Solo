package git

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/v1truv1us/solo/internal/db"
	"github.com/v1truv1us/solo/internal/output"
)

// WorktreeInfo holds info about a worktree.
type WorktreeInfo struct {
	Path             string   `json:"path"`
	TaskID           string   `json:"task_id"`
	Branch           string   `json:"branch"`
	BaseRef          string   `json:"base_ref"`
	Status           string   `json:"status"`
	GitStatus        string   `json:"git_status,omitempty"`
	UncommittedFiles []string `json:"uncommitted_files,omitempty"`
	AheadCommits     int      `json:"ahead_commits,omitempty"`
	BehindCommits    int      `json:"behind_commits,omitempty"`
	DiskUsageMB      int      `json:"disk_usage_mb,omitempty"`
	CreatedAt        string   `json:"created_at"`
	CleanedAt        *string  `json:"cleaned_at,omitempty"`
}

// CreateWorktree creates a git worktree for a session per spec §7.1.
func CreateWorktree(database *sql.DB, repoRoot, taskID, sessionID, reservationID string) (string, string, error) {
	// Step 1: Empty repo check (CRITICAL per spec §7.1)
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", "", output.NewError(output.ErrRepoEmpty,
			fmt.Sprintf("repository has no commits: %s", strings.TrimSpace(string(out))),
			false, "Make an initial commit before using worktrees")
	}

	// Check max worktrees limit
	maxWorktreesStr, _ := db.GetConfig(database, "max_worktrees")
	maxWorktrees, _ := strconv.Atoi(maxWorktreesStr)
	if maxWorktrees <= 0 {
		maxWorktrees = 5
	}

	var activeCount int
	database.QueryRow("SELECT COUNT(*) FROM worktrees WHERE status = 'active'").Scan(&activeCount)
	if activeCount >= maxWorktrees {
		return "", "", output.NewError(output.ErrWorktreeLimitExceeded,
			fmt.Sprintf("active worktrees (%d) >= max (%d)", activeCount, maxWorktrees),
			false, "Clean up with solo worktree cleanup")
	}

	// Step 2: Determine branch name
	machineID, _ := db.GetConfig(database, "machine_id")
	if machineID == "" {
		machineID = "default"
	}
	branch := fmt.Sprintf("solo/%s/%s", machineID, taskID)

	// Step 3: Determine worktree path
	worktreeDir, _ := db.GetConfig(database, "worktree_dir")
	if worktreeDir == "" {
		worktreeDir = ".solo/worktrees"
	}
	wtPath := filepath.Join(repoRoot, worktreeDir, taskID)

	// Step 4: Get base ref
	baseRef, _ := db.GetConfig(database, "base_ref")
	if baseRef == "" {
		baseRef = "origin/main"
	}

	// Verify base_ref exists
	checkRef := exec.Command("git", "rev-parse", "--verify", baseRef)
	checkRef.Dir = repoRoot
	if out, err := checkRef.CombinedOutput(); err != nil {
		// Try HEAD as fallback
		checkHead := exec.Command("git", "rev-parse", "--verify", "HEAD")
		checkHead.Dir = repoRoot
		if _, err2 := checkHead.CombinedOutput(); err2 != nil {
			return "", "", output.NewError(output.ErrBaseRefNotFound,
				fmt.Sprintf("base ref %q not found: %s", baseRef, strings.TrimSpace(string(out))),
				false, "Run git fetch or update config")
		}
		baseRef = "HEAD"
	}

	// Get base commit SHA
	baseCommitCmd := exec.Command("git", "rev-parse", baseRef)
	baseCommitCmd.Dir = repoRoot
	baseCommitOut, _ := baseCommitCmd.Output()
	baseCommitSHA := strings.TrimSpace(string(baseCommitOut))

	// Ensure parent directory exists
	os.MkdirAll(filepath.Dir(wtPath), 0755)

	// Run git worktree add with retry for index.lock
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		cmd := exec.Command("git", "worktree", "add", wtPath, "-b", branch, baseRef)
		cmd.Dir = repoRoot
		out, err := cmd.CombinedOutput()
		if err == nil {
			break
		}

		errStr := string(out)
		lastErr = fmt.Errorf("%s", strings.TrimSpace(errStr))

		// Classify error
		if strings.Contains(errStr, "index.lock") {
			if attempt < 2 {
				time.Sleep(100 * time.Millisecond * time.Duration(attempt+1))
				continue
			}
			return "", "", output.NewError(output.ErrGitIndexLocked,
				fmt.Sprintf(".git/index.lock exists after %d retries", attempt+1),
				true, "Auto-retried 3x, then surfaces as error")
		}
		if strings.Contains(errStr, "already exists") {
			if strings.Contains(errStr, "branch") || strings.Contains(errStr, "fatal: a branch named") {
				return "", "", output.NewError(output.ErrBranchExists,
					fmt.Sprintf("branch %q already exists", branch),
					false, "Use --branch flag to override")
			}
			return "", "", output.NewError(output.ErrWorktreeExists,
				fmt.Sprintf("worktree path %q already exists", wtPath),
				false, "Run solo worktree cleanup")
		}
		if strings.Contains(errStr, "No space left") {
			return "", "", output.NewError(output.ErrGitError,
				"disk full during worktree add", false, "")
		}

		return "", "", output.NewError(output.ErrWorktreeError,
			fmt.Sprintf("git worktree add failed: %s", lastErr), false, "Check git output in message")
	}

	// Step 5-7: Insert worktree record and update session/reservation with connection pinning
	err := db.WithTxImmediate(database, func(conn *sql.Conn) error {
		ctx := context.Background()
		_, err := conn.ExecContext(ctx, `INSERT INTO worktrees (path, task_id, branch_name, base_ref, base_commit_sha, status)
			VALUES (?, ?, ?, ?, ?, 'active')`,
			wtPath, taskID, branch, baseRef, baseCommitSHA)
		if err != nil {
			return err
		}
		conn.ExecContext(ctx, "UPDATE sessions SET worktree_path = ?, branch = ? WHERE id = ?", wtPath, branch, sessionID)
		conn.ExecContext(ctx, "UPDATE reservations SET worktree_path = ? WHERE id = ?", wtPath, reservationID)
		return nil
	})
	if err != nil {
		return "", "", err
	}

	return wtPath, branch, nil
}

// CleanupWorktree removes a worktree per spec §7.2.
func CleanupWorktree(database *sql.DB, repoRoot, taskID string, force bool) ([]string, []string, error) {
	var where string
	var args []interface{}

	if taskID != "" {
		where = "WHERE w.task_id = ? AND w.status != 'cleaned'"
		args = append(args, taskID)
	} else {
		where = "WHERE w.status != 'cleaned'"
	}

	rows, err := database.Query(fmt.Sprintf(`
		SELECT w.path, w.task_id, w.branch_name, w.status
		FROM worktrees w %s`, where), args...)
	if err != nil {
		return nil, nil, fmt.Errorf("querying worktrees: %w", err)
	}
	defer rows.Close()

	var cleaned, skipped []string
	for rows.Next() {
		var path, tid, branch, status string
		rows.Scan(&path, &tid, &branch, &status)

		// Skip if there is an active reservation or session for task_id
		var activeCount int
		database.QueryRow(`SELECT COUNT(*) FROM reservations WHERE task_id = ? AND active = 1`, tid).Scan(&activeCount)
		if activeCount > 0 {
			skipped = append(skipped, path+" (active reservation)")
			continue
		}
		database.QueryRow(`SELECT COUNT(*) FROM sessions WHERE task_id = ? AND ended_at IS NULL`, tid).Scan(&activeCount)
		if activeCount > 0 {
			skipped = append(skipped, path+" (active session)")
			continue
		}

		// Check if worktree is dirty (invariant #10)
		if !force {
			cmd := exec.Command("git", "status", "--porcelain")
			cmd.Dir = path
			out, err := cmd.Output()
			if err == nil && len(strings.TrimSpace(string(out))) > 0 {
				skipped = append(skipped, path+" (dirty worktree, use --force)")
				continue
			}
		}

		// Remove worktree
		removeArgs := []string{"worktree", "remove", path}
		if force {
			removeArgs = []string{"worktree", "remove", "--force", path}
		}
		cmd := exec.Command("git", removeArgs...)
		cmd.Dir = repoRoot
		cmd.CombinedOutput() // ignore errors

		// Delete branch
		if force {
			exec.Command("git", "-C", repoRoot, "branch", "-D", branch).CombinedOutput()
		} else {
			exec.Command("git", "-C", repoRoot, "branch", "-d", branch).CombinedOutput()
		}

		// Update DB with connection pinning
		db.WithTxImmediate(database, func(conn *sql.Conn) error {
			ctx := context.Background()
			conn.ExecContext(ctx, `UPDATE worktrees SET status = 'cleaned', cleaned_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE path = ?`, path)
			return nil
		})

		cleaned = append(cleaned, path)
	}

	return cleaned, skipped, nil
}

// ListWorktrees lists all worktrees from the database.
func ListWorktrees(database *sql.DB, statusFilter string) ([]WorktreeInfo, error) {
	var where string
	var args []interface{}
	if statusFilter != "" {
		where = "WHERE w.status = ?"
		args = append(args, statusFilter)
	}

	query := fmt.Sprintf(`
		SELECT w.path, w.task_id, w.branch_name, w.base_ref, w.status,
			w.disk_usage_bytes, w.created_at, w.cleaned_at
		FROM worktrees w %s ORDER BY w.created_at`, where)

	rows, err := database.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing worktrees: %w", err)
	}
	defer rows.Close()

	var worktrees []WorktreeInfo
	for rows.Next() {
		var w WorktreeInfo
		var diskBytes sql.NullInt64
		rows.Scan(&w.Path, &w.TaskID, &w.Branch, &w.BaseRef, &w.Status,
			&diskBytes, &w.CreatedAt, &w.CleanedAt)
		if diskBytes.Valid {
			w.DiskUsageMB = int(diskBytes.Int64 / (1024 * 1024))
		}
		worktrees = append(worktrees, w)
	}
	if worktrees == nil {
		worktrees = []WorktreeInfo{}
	}
	return worktrees, nil
}

// InspectWorktree inspects a worktree by task ID.
func InspectWorktree(database *sql.DB, repoRoot, identifier string) (*WorktreeInfo, error) {
	var w WorktreeInfo
	var diskBytes sql.NullInt64

	// Try by task_id first, then by path
	err := database.QueryRow(`
		SELECT path, task_id, branch_name, base_ref, status, disk_usage_bytes, created_at, cleaned_at
		FROM worktrees WHERE task_id = ? OR path = ?
		ORDER BY created_at DESC LIMIT 1`, identifier, identifier,
	).Scan(&w.Path, &w.TaskID, &w.Branch, &w.BaseRef, &w.Status,
		&diskBytes, &w.CreatedAt, &w.CleanedAt)
	if err == sql.ErrNoRows {
		return nil, output.NewError(output.ErrTaskNotFound,
			fmt.Sprintf("no worktree found for %q", identifier), false, "")
	}
	if err != nil {
		return nil, fmt.Errorf("querying worktree: %w", err)
	}
	if diskBytes.Valid {
		w.DiskUsageMB = int(diskBytes.Int64 / (1024 * 1024))
	}

	// Get git status if worktree path exists
	if _, err := os.Stat(w.Path); err == nil {
		cmd := exec.Command("git", "status", "--porcelain")
		cmd.Dir = w.Path
		out, err := cmd.Output()
		if err == nil {
			if len(strings.TrimSpace(string(out))) == 0 {
				w.GitStatus = "clean"
			} else {
				w.GitStatus = "dirty"
				for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
					if line != "" {
						w.UncommittedFiles = append(w.UncommittedFiles, strings.TrimSpace(line))
					}
				}
			}
		}

		// Check ahead/behind
		revList := exec.Command("git", "rev-list", "--left-right", "--count", w.BaseRef+"...HEAD")
		revList.Dir = w.Path
		revOut, err := revList.Output()
		if err == nil {
			parts := strings.Fields(string(revOut))
			if len(parts) == 2 {
				w.BehindCommits, _ = strconv.Atoi(parts[0])
				w.AheadCommits, _ = strconv.Atoi(parts[1])
			}
		}
	}

	return &w, nil
}
