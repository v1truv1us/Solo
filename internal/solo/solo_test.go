package solo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
		"meta":                 map[string]any{"token_budget": 10},
		"system_directives":    map[string]any{"trust_policy": "x", "worktree_rule": "y", "completion_rule": "z"},
		"task":                 map[string]any{"id": "T-1", "title": "Title", "description": strings.Repeat("many words ", 100), "status": "ready", "type": "task", "priority": 3, "acceptance_criteria": "a", "definition_of_done": "b", "affected_files": []string{"a.go"}},
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
