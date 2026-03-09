package solo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := openDB(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := applySchema(db); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	if err := setDefaultConfig(db, "test-machine"); err != nil {
		t.Fatalf("config: %v", err)
	}
	return db
}

func TestTransitionMatrixRejectsInvalid(t *testing.T) {
	if transitionAllowed("open", "done") {
		t.Fatalf("open -> done must be invalid")
	}
	if !transitionAllowed("open", "ready") {
		t.Fatalf("open -> ready must be valid")
	}
}

func TestSanitizeUntrusted(t *testing.T) {
	in := "abc\x00\x1b[31mred\x1b[0m"
	out := sanitizeUntrusted(in)
	if out == in {
		t.Fatalf("expected sanitization to modify input")
	}
	if out == "" {
		t.Fatalf("expected non-empty output")
	}
}

func TestInvalidTransitionErrorDetails(t *testing.T) {
	err := errInvalidTransition("open", "done", []string{"triaged", "ready", "cancelled"})
	if err.Code != "INVALID_TRANSITION" {
		t.Fatalf("unexpected code: %s", err.Code)
	}
	if err.CurrentStatus != "open" || err.RequestedStatus != "done" {
		t.Fatalf("missing transition detail fields")
	}
	if len(err.ValidTransitions) != 3 {
		t.Fatalf("expected valid transitions list")
	}
}

func TestTokenBudgetTruncatesLowPriorityFields(t *testing.T) {
	bundle := map[string]any{
		"meta": map[string]any{"token_budget": 10},
		"system_directives": map[string]any{"trust_policy": "x", "worktree_rule": "y", "completion_rule": "z"},
		"task": map[string]any{"id": "T-1", "title": "Title", "description": strings.Repeat("many words ", 100), "status": "ready", "type": "task", "priority": 3, "acceptance_criteria": "a", "definition_of_done": "b", "affected_files": []string{"a.go"}},
		"reservation":          map[string]any{"id": "r"},
		"worktree":             map[string]any{"path": ".solo/worktrees/T-1"},
		"dependencies":         []map[string]any{{"task_id": "T-0"}},
		"latest_handoff":       map[string]any{"summary": "h"},
		"recent_sessions":      []map[string]any{{"notes": strings.Repeat("session ", 200)}},
		"error_history":        []any{"e1"},
		"duplicate_candidates": []map[string]any{{"task_id": "T-2"}},
		"warnings":             []any{},
		"truncation":           map[string]any{"sessions_total": 1, "sessions_included": 1, "handoffs_total": 1, "handoffs_included": 1},
	}
	out := enforceTokenBudget(bundle, 20)
	if dc, ok := out["duplicate_candidates"].([]map[string]any); ok && len(dc) > 0 {
		t.Fatalf("expected duplicate candidates truncation")
	}
}

func TestRepoDiscoveryUsesParentGit(t *testing.T) {
	tmp := t.TempDir()
	if err := os.Mkdir(filepath.Join(tmp, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	deep := filepath.Join(tmp, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatalf("mkdir deep: %v", err)
	}
	root, err := discoverRepoRoot(deep)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if root != tmp {
		t.Fatalf("expected %s got %s", tmp, root)
	}
}

// TestTaskIDSequence verifies that task IDs are assigned as T-1, T-2, T-3 …
// in strict insertion order and that the T-N format is correct.
func TestTaskIDSequence(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	ctx := context.Background()

	const n = 3
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		if err := withImmediateTx(ctx, db, func(conn *sql.Conn) error {
			var id string
			if err := conn.QueryRowContext(ctx, `SELECT 'T-' || (COALESCE((SELECT MAX(CAST(SUBSTR(id,3) AS INTEGER)) FROM tasks),0)+1)`).Scan(&id); err != nil {
				return err
			}
			_, err := conn.ExecContext(ctx,
				`INSERT INTO tasks (id,title,type,priority) VALUES (?,?,?,?)`,
				id, fmt.Sprintf("Task %d", i+1), "task", 3)
			if err != nil {
				return err
			}
			ids[i] = id
			return nil
		}); err != nil {
			t.Fatalf("insert task %d: %v", i+1, err)
		}
	}

	for i, id := range ids {
		want := fmt.Sprintf("T-%d", i+1)
		if id != want {
			t.Errorf("task[%d]: got %q, want %q", i, id, want)
		}
	}
}

// TestCircularDependencyDetected checks that ensureNoCycle returns
// CIRCULAR_DEPENDENCY when adding a dep would form a cycle (A→B, then B→A).
func TestCircularDependencyDetected(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	ctx := context.Background()

	// Insert two tasks directly.
	for _, id := range []string{"T-1", "T-2"} {
		if _, err := db.Exec(`INSERT INTO tasks (id,title,type,priority) VALUES (?,?,?,?)`, id, id, "task", 3); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}

	// Add T-2 depends on T-1 (no cycle yet).
	if _, err := db.Exec(`INSERT INTO task_dependencies (task_id,depends_on) VALUES ('T-2','T-1')`); err != nil {
		t.Fatalf("insert dep: %v", err)
	}

	// Now try T-1 depends on T-2 — should create a cycle.
	var cycleErr error
	if err := withImmediateTx(ctx, db, func(conn *sql.Conn) error {
		cycleErr = ensureNoCycle(ctx, conn, "T-1", "T-2")
		return nil // don't abort the tx; we just want to capture the error
	}); err != nil {
		t.Fatalf("tx error: %v", err)
	}

	if cycleErr == nil {
		t.Fatal("expected CIRCULAR_DEPENDENCY error, got nil")
	}
	var soloErr *Error
	if !errors.As(cycleErr, &soloErr) || soloErr.Code != "CIRCULAR_DEPENDENCY" {
		t.Fatalf("expected CIRCULAR_DEPENDENCY, got %v", cycleErr)
	}
}

// TestAllErrorCodes ensures every error-code constant is non-empty.
// This catches typos and prevents accidental empty-string codes from slipping
// through. The list must include every code defined in errors.go and git.go.
func TestAllErrorCodes(t *testing.T) {
	allErrors := []*Error{
		ErrInvalidArgument("x"),
		errNotRepo(),
		errTaskNotFound("T-1"),
		errTaskNotReady("blocked"),
		errTaskLocked("T-1"),
		errNoActiveSession("T-1"),
		errVersionConflict(),
		errInvalidTransition("open", "done", nil),
		errCircularDependency(),
		errRepoEmpty(),
		errWorktreeDirty("/tmp/wt"),
		errWorktreeExists("/tmp/wt"),
		errWorktreeLimitExceeded(5),
		errGitIndexLocked(),
		errGitError("git failed"),
		errBaseRefNotFound("origin/main"),
		errAlreadyInitialized(),
		errSQLiteBusy(),
		errHandoffLocked(),
		errWorktreeError("something broke"),
		errBranchExists("solo/main/T-1"),
	}
	for _, e := range allErrors {
		if e.Code == "" {
			t.Errorf("error with message %q has an empty code", e.Message)
		}
	}
}
