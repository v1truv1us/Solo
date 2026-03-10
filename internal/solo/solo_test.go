package solo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTransitionMatrixRejectsInvalid(t *testing.T) {
	if transitionAllowed("draft", "completed") {
		t.Fatalf("draft -> completed must be invalid")
	}
	if !transitionAllowed("draft", "ready") {
		t.Fatalf("draft -> ready must be valid")
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
	err := errInvalidTransition("draft", "completed", []string{"ready", "blocked", "cancelled"})
	if err.Code != "INVALID_TRANSITION" {
		t.Fatalf("unexpected code: %s", err.Code)
	}
	if err.CurrentStatus != "draft" || err.RequestedStatus != "completed" {
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

func TestSemanticCompatibilityMappings(t *testing.T) {
	if got := canonicalTaskStatus("in_progress"); got != "active" {
		t.Fatalf("expected active, got %s", got)
	}
	if got := parsePriorityValue("1", 0); got != 2 {
		t.Fatalf("expected low(2), got %d", got)
	}
	if got := priorityLabel(5); got != "critical" {
		t.Fatalf("expected critical, got %s", got)
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

func TestInstallSoloSkillScopes(t *testing.T) {
	tmp := t.TempDir()
	envPath, err := installSoloSkill(tmp, "environment", "")
	if err != nil {
		t.Fatalf("install env skill: %v", err)
	}
	if _, err := os.Stat(envPath); err != nil {
		t.Fatalf("env skill missing: %v", err)
	}
	agentPath, err := installSoloSkill(tmp, "agent", "opencode")
	if err != nil {
		t.Fatalf("install agent skill: %v", err)
	}
	if _, err := os.Stat(agentPath); err != nil {
		t.Fatalf("agent skill missing: %v", err)
	}
}

func TestInstallSoloSkillRequiresAgentWhenScoped(t *testing.T) {
	tmp := t.TempDir()
	if _, err := installSoloSkill(tmp, "agent", ""); err == nil {
		t.Fatalf("expected error for missing agent")
	}
}
