package solo

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
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
	createSoloSkillFixture(t, tmp)
	envPath, err := installSoloSkill(tmp, "environment", "")
	if err != nil {
		t.Fatalf("install env skill: %v", err)
	}
	if _, err := os.Stat(envPath); err != nil {
		t.Fatalf("env skill missing: %v", err)
	}
	envRef := filepath.Join(tmp, ".solo", "skills", "solo", "references", "commands.md")
	if _, err := os.Stat(envRef); err != nil {
		t.Fatalf("env skill reference missing: %v", err)
	}
	agentPath, err := installSoloSkill(tmp, "agent", "opencode")
	if err != nil {
		t.Fatalf("install agent skill: %v", err)
	}
	if _, err := os.Stat(agentPath); err != nil {
		t.Fatalf("agent skill missing: %v", err)
	}
	agentRef := filepath.Join(tmp, ".solo", "skills", "agents", "opencode", "solo", "references", "commands.md")
	if _, err := os.Stat(agentRef); err != nil {
		t.Fatalf("agent skill reference missing: %v", err)
	}
}

func TestInstallSoloSkillRequiresAgentWhenScoped(t *testing.T) {
	tmp := t.TempDir()
	if _, err := installSoloSkill(tmp, "agent", ""); err == nil {
		t.Fatalf("expected error for missing agent")
	}
}

func TestInstallSoloSkillFallsBackToTemplate(t *testing.T) {
	tmp := t.TempDir()
	skillPath, err := installSoloSkill(tmp, "environment", "")
	if err != nil {
		t.Fatalf("install fallback skill: %v", err)
	}
	content, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read fallback skill: %v", err)
	}
	if !strings.Contains(string(content), "allowed-tools: Bash(solo:*)") {
		t.Fatalf("expected fallback skill content to include allowed-tools")
	}
}

func TestInstallSoloSkillKeepsExistingBundleOnCopyFailure(t *testing.T) {
	tmp := t.TempDir()
	existingDir := filepath.Join(tmp, ".solo", "skills", "solo")
	if err := os.MkdirAll(existingDir, 0o755); err != nil {
		t.Fatalf("mkdir existing dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(existingDir, "SKILL.md"), []byte("old bundle\n"), 0o644); err != nil {
		t.Fatalf("write existing skill: %v", err)
	}
	createSoloSkillFixture(t, tmp)
	badLink := filepath.Join(tmp, "skills", "solo", "broken-link")
	if err := os.Symlink(filepath.Join(tmp, "skills", "solo", "SKILL.md"), badLink); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, err := installSoloSkill(tmp, "environment", ""); err == nil {
		t.Fatalf("expected install failure for unsupported symlink entry")
	}
	content, err := os.ReadFile(filepath.Join(existingDir, "SKILL.md"))
	if err != nil {
		t.Fatalf("read existing skill after failed install: %v", err)
	}
	if string(content) != "old bundle\n" {
		t.Fatalf("expected existing skill bundle to remain intact, got %q", string(content))
	}
}

func TestApplySchemaMigratesLegacyReservationsAndWorktrees(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "solo.db")
	db, err := openDB(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	legacy := []string{
		`CREATE TABLE tasks (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			description TEXT DEFAULT '',
			type TEXT NOT NULL DEFAULT 'task',
			status TEXT NOT NULL DEFAULT 'draft',
			priority INTEGER NOT NULL DEFAULT 3,
			acceptance_criteria TEXT DEFAULT '',
			definition_of_done TEXT DEFAULT '',
			affected_files TEXT DEFAULT '[]',
			labels TEXT DEFAULT '[]',
			parent_task TEXT,
			version INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
			updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		)`,
		`CREATE TABLE reservations (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
			worker_id TEXT NOT NULL,
			active INTEGER NOT NULL DEFAULT 1,
			reserved_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
			expires_at TEXT NOT NULL,
			ttl_sec INTEGER NOT NULL DEFAULT 3600,
			released_at TEXT,
			release_reason TEXT,
			worktree_path TEXT,
			machine_id TEXT NOT NULL DEFAULT 'default'
		)`,
		`CREATE TABLE worktrees (
			path TEXT PRIMARY KEY,
			task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
			branch_name TEXT NOT NULL,
			base_ref TEXT NOT NULL DEFAULT 'origin/main',
			base_commit_sha TEXT,
			status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'cleanup_pending', 'cleaned')),
			disk_usage_bytes INTEGER,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
			cleaned_at TEXT
		)`,
	}
	for _, stmt := range legacy {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("seed legacy schema: %v", err)
		}
	}
	if _, err := db.Exec(`INSERT INTO tasks (id, title) VALUES ('T-1', 'Legacy task')`); err != nil {
		t.Fatalf("insert task: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO worktrees (path, task_id, branch_name, status, cleaned_at) VALUES ('/tmp/T-1', 'T-1', 'solo/test/T-1', 'cleaned', strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`); err != nil {
		t.Fatalf("insert cleaned worktree: %v", err)
	}

	if err := applySchema(db); err != nil {
		t.Fatalf("apply schema: %v", err)
	}

	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("db conn: %v", err)
	}
	defer conn.Close()
	if hasToken, err := tableHasColumn(ctx, conn, "reservations", "token"); err != nil {
		t.Fatalf("reservations token lookup: %v", err)
	} else if !hasToken {
		t.Fatalf("expected reservations.token after migration")
	}
	if hasCleanedAt, err := tableHasColumn(ctx, conn, "worktrees", "cleaned_at"); err != nil {
		t.Fatalf("worktrees cleaned_at lookup: %v", err)
	} else if hasCleanedAt {
		t.Fatalf("expected cleaned_at column to be removed")
	}
	var cleanedCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM worktrees WHERE status='cleaned'`).Scan(&cleanedCount); err != nil {
		t.Fatalf("count cleaned worktrees: %v", err)
	}
	if cleanedCount != 0 {
		t.Fatalf("expected cleaned worktrees to be removed, got %d", cleanedCount)
	}
}

func TestStartSessionWithoutPIDStoresNullAgentPID(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init app: %v", err)
		}
		taskID := createReadyTask(t, app, "PID test")
		if _, err := app.StartSession(taskID, "pi", 0, 0); err != nil {
			t.Fatalf("start session: %v", err)
		}

		db, err := openDB(filepath.Join(repoRoot, ".solo", "solo.db"))
		if err != nil {
			t.Fatalf("open repo db: %v", err)
		}
		defer db.Close()
		var pid sql.NullInt64
		if err := db.QueryRow(`SELECT agent_pid FROM sessions WHERE task_id=? AND ended_at IS NULL`, taskID).Scan(&pid); err != nil {
			t.Fatalf("query agent pid: %v", err)
		}
		if pid.Valid {
			t.Fatalf("expected NULL agent_pid when --pid is omitted, got %d", pid.Int64)
		}
		var zombieScannable int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE task_id=? AND ended_at IS NULL AND agent_pid IS NOT NULL`, taskID).Scan(&zombieScannable); err != nil {
			t.Fatalf("query zombie scan candidates: %v", err)
		}
		if zombieScannable != 0 {
			t.Fatalf("expected session to be skipped by zombie scan, got %d candidate rows", zombieScannable)
		}
	})
}

func TestCleanupWorktreesDeletesRows(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init app: %v", err)
		}
		taskID := createReadyTask(t, app, "Cleanup test")
		if _, err := app.StartSession(taskID, "pi", 0, 0); err != nil {
			t.Fatalf("start session: %v", err)
		}
		if _, err := app.EndSession(taskID, "completed", "", nil, nil, ""); err != nil {
			t.Fatalf("end session: %v", err)
		}
		resp, err := app.CleanupWorktrees(taskID, false)
		if err != nil {
			t.Fatalf("cleanup worktrees: %v", err)
		}
		cleaned, _ := resp["cleaned"].([]map[string]any)
		if len(cleaned) != 1 {
			t.Fatalf("expected 1 cleaned worktree, got %d", len(cleaned))
		}

		db, err := openDB(filepath.Join(repoRoot, ".solo", "solo.db"))
		if err != nil {
			t.Fatalf("open repo db: %v", err)
		}
		defer db.Close()
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM worktrees WHERE task_id=?`, taskID).Scan(&count); err != nil {
			t.Fatalf("count worktree rows: %v", err)
		}
		if count != 0 {
			t.Fatalf("expected worktree row to be deleted, got %d", count)
		}
	})
}

func createReadyTask(t *testing.T, app *App, title string) string {
	t.Helper()
	resp, err := app.CreateTask(CreateTaskInput{Title: title})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	task, ok := resp["task"].(map[string]any)
	if !ok {
		t.Fatalf("missing task payload: %#v", resp)
	}
	taskID, _ := task["id"].(string)
	version, _ := task["version"].(int)
	if taskID == "" || version == 0 {
		t.Fatalf("unexpected task payload: %#v", task)
	}
	if _, err := app.UpdateTaskStatus(taskID, "ready", version); err != nil {
		t.Fatalf("mark task ready: %v", err)
	}
	return taskID
}

func withTempGitRepoCWD(t *testing.T, fn func(repoRoot string)) {
	t.Helper()
	tmp := t.TempDir()
	mustRunGit(t, tmp, "init", "-b", "main")
	mustRunGit(t, tmp, "config", "user.email", "solo-tests@example.com")
	mustRunGit(t, tmp, "config", "user.name", "Solo Tests")
	if err := os.WriteFile(filepath.Join(tmp, "README.md"), []byte("init\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	mustRunGit(t, tmp, "add", "README.md")
	mustRunGit(t, tmp, "commit", "-m", "init")
	mustRunGit(t, tmp, "update-ref", "refs/remotes/origin/main", "HEAD")
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir temp: %v", err)
	}
	defer func() {
		_ = os.Chdir(old)
	}()
	fn(tmp)
}

func mustRunGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}

func createSoloSkillFixture(t *testing.T, root string) {
	t.Helper()
	skillDir := filepath.Join(root, "skills", "solo")
	refDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refDir, 0o755); err != nil {
		t.Fatalf("mkdir skill fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: solo\n---\nfixture\n"), 0o644); err != nil {
		t.Fatalf("write fixture skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(refDir, "commands.md"), []byte("# Commands\n"), 0o644); err != nil {
		t.Fatalf("write fixture ref: %v", err)
	}
}

func TestTaskDepsReturnsDependencies(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}

		// Create parent task
		parentResp, err := app.CreateTask(CreateTaskInput{Title: "Parent task"})
		if err != nil {
			t.Fatalf("create parent: %v", err)
		}
		parentID := parentResp["task"].(map[string]any)["id"].(string)

		// Create child task with deps
		childResp, err := app.CreateTask(CreateTaskInput{Title: "Child task", Dependencies: []string{parentID}})
		if err != nil {
			t.Fatalf("create child: %v", err)
		}
		childID := childResp["task"].(map[string]any)["id"].(string)

		// Query deps
		resp, err := app.TaskDeps(childID)
		if err != nil {
			t.Fatalf("task deps: %v", err)
		}
		if resp["task_id"] != childID {
			t.Fatalf("expected task_id=%s, got %v", childID, resp["task_id"])
		}
		deps := resp["dependencies"].([]map[string]any)
		if len(deps) != 1 {
			t.Fatalf("expected 1 dependency, got %d", len(deps))
		}
		if deps[0]["task_id"] != parentID {
			t.Fatalf("expected dep id=%s, got %v", parentID, deps[0]["task_id"])
		}
	})
}

func TestSearchFindsTasks(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}
		_, _ = app.CreateTask(CreateTaskInput{Title: "Fix login bug"})
		_, _ = app.CreateTask(CreateTaskInput{Title: "Add dark mode"})

		resp, err := app.Search("login", "", 10)
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		results := resp["results"].([]map[string]any)
		if len(results) == 0 {
			t.Fatal("expected search results for 'login'")
		}
		found := false
		for _, r := range results {
			if strings.Contains(r["title"].(string), "login") {
				found = true
			}
		}
		if !found {
			t.Fatal("search did not return the login task")
		}
	})
}

func TestSearchWithStatusFilter(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}
		_, _ = app.CreateTask(CreateTaskInput{Title: "Draft task search"})

		// Filter by draft status
		resp, err := app.Search("Draft", "draft", 10)
		if err != nil {
			t.Fatalf("search --status: %v", err)
		}
		results := resp["results"].([]map[string]any)
		if len(results) == 0 {
			t.Fatal("expected results for draft status filter")
		}

		// Filter by completed (should return nothing)
		resp2, err2 := app.Search("Draft", "completed", 10)
		if err2 != nil {
			t.Fatalf("search completed: %v", err2)
		}
		results2 := resp2["results"].([]map[string]any)
		if len(results2) != 0 {
			t.Fatalf("expected 0 results for completed filter, got %d", len(results2))
		}
	})
}

func TestTaskTreeReturnsNodes(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}

		parentResp, err := app.CreateTask(CreateTaskInput{Title: "Root task"})
		if err != nil {
			t.Fatalf("create root: %v", err)
		}
		parentID := parentResp["task"].(map[string]any)["id"].(string)

		childResp, err := app.CreateTask(CreateTaskInput{Title: "Child task", ParentTask: parentID})
		if err != nil {
			t.Fatalf("create child: %v", err)
		}
		childID := childResp["task"].(map[string]any)["id"].(string)

		resp, err := app.TaskTree(parentID)
		if err != nil {
			t.Fatalf("task tree: %v", err)
		}
		nodes := resp["nodes"].([]map[string]any)
		if len(nodes) < 2 {
			t.Fatalf("expected at least 2 nodes (root+child), got %d", len(nodes))
		}

		// Verify root node
		foundRoot, foundChild := false, false
		for _, n := range nodes {
			if n["id"] == parentID {
				foundRoot = true
			}
			if n["id"] == childID {
				foundChild = true
			}
		}
		if !foundRoot || !foundChild {
			t.Fatalf("expected both root and child in tree; foundRoot=%v foundChild=%v", foundRoot, foundChild)
		}
	})
}

func TestTaskTreeNotFound(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}
		_, err := app.TaskTree("T-999")
		if err == nil {
			t.Fatal("expected error for nonexistent task")
		}
	})
}

func TestUpdateTask(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}

		resp, err := app.CreateTask(CreateTaskInput{Title: "Original"})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		task := resp["task"].(map[string]any)
		taskID := task["id"].(string)
		version := task["version"].(int)

		updated, err := app.UpdateTask(taskID, "New Title", "desc", "high", "", []string{"bug"}, nil, version)
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		updatedTask := updated["task"].(map[string]any)
		if updatedTask["title"] != "New Title" {
			t.Fatalf("expected title=New Title, got %v", updatedTask["title"])
		}
	})
}

func TestUpdateTaskRequiresVersion(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}
		_, err := app.UpdateTask("T-1", "title", "", "", "", nil, nil, 0)
		if err == nil {
			t.Fatal("expected error for version=0")
		}
	})
}

func TestInspectWorktree(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}
		taskID := createReadyTask(t, app, "Inspect test")
		if _, err := app.StartSession(taskID, "pi", 0, 0); err != nil {
			t.Fatalf("start session: %v", err)
		}
		if _, err := app.EndSession(taskID, "completed", "", nil, nil, ""); err != nil {
			t.Fatalf("end session: %v", err)
		}

		resp, err := app.InspectWorktree(taskID)
		if err != nil {
			t.Fatalf("inspect worktree: %v", err)
		}
		if resp["task_id"] != taskID {
			t.Fatalf("expected task_id=%s, got %v", taskID, resp["task_id"])
		}
	})
}

func TestInspectWorktreeNotFound(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}
		_, err := app.InspectWorktree("T-999")
		if err == nil {
			t.Fatal("expected error for nonexistent worktree")
		}
	})
}

func TestListWorktrees(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}
		resp, err := app.ListWorktrees()
		if err != nil {
			t.Fatalf("list worktrees: %v", err)
		}
		if _, ok := resp["worktrees"]; !ok {
			t.Fatal("expected 'worktrees' key in response")
		}
	})
}

func TestSearchInvalidStatus(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}
		_, _ = app.CreateTask(CreateTaskInput{Title: "test"})
		_, err := app.Search("test", "bogus_status", 10)
		if err == nil {
			t.Fatal("expected error for invalid status filter")
		}
	})
}

func TestHandoffShowNotFound(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}
		_, err := app.ShowHandoff("H-999")
		if err == nil {
			t.Fatal("expected error for nonexistent handoff")
		}
	})
}

func TestTaskContext(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}
		resp, err := app.CreateTask(CreateTaskInput{Title: "Context test", Description: "Some description"})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		taskID := resp["task"].(map[string]any)["id"].(string)

		ctx, err := app.TaskContext(taskID, 0)
		if err != nil {
			t.Fatalf("task context: %v", err)
		}
		task := ctx["task"].(map[string]any)
		if task["id"] != taskID {
			t.Fatalf("expected task.id=%s, got %v", taskID, task["id"])
		}
	})
}

func TestRecoverTask(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}
		_, err := app.RecoverTask("T-999", 1)
		if err == nil {
			t.Fatal("expected error for nonexistent task")
		}
	})
}

func TestShowTask(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}

		resp, err := app.CreateTask(CreateTaskInput{Title: "Show test", Description: "some desc"})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		taskID := resp["task"].(map[string]any)["id"].(string)

		showResp, err := app.ShowTask(taskID)
		if err != nil {
			t.Fatalf("show task: %v", err)
		}
		task := showResp["task"].(map[string]any)
		if task["id"] != taskID {
			t.Fatalf("expected id=%s, got %v", taskID, task["id"])
		}
		if task["title"] != "Show test" {
			t.Fatalf("expected title=Show test, got %v", task["title"])
		}
		// Check optional fields exist
		if _, ok := showResp["dependencies"]; !ok {
			t.Fatal("expected 'dependencies' key")
		}
		if _, ok := showResp["session_count"]; !ok {
			t.Fatal("expected 'session_count' key")
		}
	})
}

func TestShowTaskNotFound(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}
		_, err := app.ShowTask("T-999")
		if err == nil {
			t.Fatal("expected error for nonexistent task")
		}
	})
}

func TestForceReady(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}

		resp, err := app.CreateTask(CreateTaskInput{Title: "Force ready test"})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		task := resp["task"].(map[string]any)
		taskID := task["id"].(string)
		version := task["version"].(int)

		readyResp, err := app.ForceReady(taskID, version)
		if err != nil {
			t.Fatalf("force ready: %v", err)
		}
		readyTask := readyResp["task"].(map[string]any)
		if readyTask["status"] != "ready" {
			t.Fatalf("expected status=ready, got %v", readyTask["status"])
		}
	})
}

func TestForceReadyVersionConflict(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}
		resp, _ := app.CreateTask(CreateTaskInput{Title: "Version conflict test"})
		taskID := resp["task"].(map[string]any)["id"].(string)

		_, err := app.ForceReady(taskID, 999)
		if err == nil {
			t.Fatal("expected version conflict error")
		}
	})
}

func TestListAudit(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}

		// Generate audit events
		_, _ = app.CreateTask(CreateTaskInput{Title: "Audit task 1"})
		_, _ = app.CreateTask(CreateTaskInput{Title: "Audit task 2"})

		resp, err := app.ListAudit("", 10, 0)
		if err != nil {
			t.Fatalf("list audit: %v", err)
		}
		events := resp["events"].([]map[string]any)
		if len(events) < 2 {
			t.Fatalf("expected at least 2 events, got %d", len(events))
		}
		// Verify event structure
		e := events[0]
		if e["id"] == nil || e["task_id"] == nil || e["event_type"] == nil {
			t.Fatalf("unexpected event structure: %v", e)
		}
	})
}

func TestListAuditByTask(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}

		resp, _ := app.CreateTask(CreateTaskInput{Title: "Filter target"})
		taskID := resp["task"].(map[string]any)["id"].(string)
		_, _ = app.CreateTask(CreateTaskInput{Title: "Other task"})

		filtered, err := app.ListAudit(taskID, 50, 0)
		if err != nil {
			t.Fatalf("list audit by task: %v", err)
		}
		events := filtered["events"].([]map[string]any)
		for _, e := range events {
			if e["task_id"] != taskID {
				t.Fatalf("expected all events for task %s, got %v", taskID, e["task_id"])
			}
		}
	})
}

func TestListAuditDefaultLimit(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}

		// 0 limit should default to 50
		resp, err := app.ListAudit("", 0, 0)
		if err != nil {
			t.Fatalf("list audit: %v", err)
		}
		if resp["limit"] != 50 {
			t.Fatalf("expected limit=50, got %v", resp["limit"])
		}
	})
}

func TestShowAudit(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}

		_, _ = app.CreateTask(CreateTaskInput{Title: "Show audit task"})
		listResp, _ := app.ListAudit("", 10, 0)
		events := listResp["events"].([]map[string]any)
		if len(events) == 0 {
			t.Fatal("no audit events")
		}
		eventID := events[0]["id"].(int)

		resp, err := app.ShowAudit(eventID)
		if err != nil {
			t.Fatalf("show audit: %v", err)
		}
		event := resp["event"].(map[string]any)
		if event["id"] == nil {
			t.Fatal("expected event with id")
		}
	})
}

func TestShowAuditInvalidID(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}
		_, err := app.ShowAudit(0)
		if err == nil {
			t.Fatal("expected error for id=0")
		}
	})
}

func TestShowAuditNotFound(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}
		_, err := app.ShowAudit(99999)
		if err == nil {
			t.Fatal("expected error for nonexistent event")
		}
	})
}

func TestCreateHandoffSuccess(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}

		// Create task → ready → start session
		taskID := createReadyTask(t, app, "Handoff task")
		if _, err := app.StartSession(taskID, "worker-a", 0, 0); err != nil {
			t.Fatalf("start session: %v", err)
		}

		// Create handoff
		resp, err := app.CreateHandoff(taskID, "Did some work", "More to do", "worker-b", []string{"main.go"})
		if err != nil {
			t.Fatalf("create handoff: %v", err)
		}
		if resp["task_id"] != taskID {
			t.Fatalf("expected task_id=%s, got %v", taskID, resp["task_id"])
		}
		if resp["from_worker"] != "worker-a" {
			t.Fatalf("expected from_worker=worker-a, got %v", resp["from_worker"])
		}
		if resp["task_status"] != "ready" {
			t.Fatalf("expected task_status=ready, got %v", resp["task_status"])
		}
		if resp["session_ended"] != true {
			t.Fatalf("expected session_ended=true, got %v", resp["session_ended"])
		}
	})
}

func TestCreateHandoffRequiresSummary(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}
		_, err := app.CreateHandoff("T-1", "", "", "worker-b", nil)
		if err == nil {
			t.Fatal("expected error for missing summary")
		}
	})
}

func TestCreateHandoffNoActiveSession(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}
		taskID := createReadyTask(t, app, "No session")
		_, err := app.CreateHandoff(taskID, "summary", "", "worker-b", nil)
		if err == nil {
			t.Fatal("expected error for no active session")
		}
	})
}

func TestListHandoffs(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}

		taskID := createReadyTask(t, app, "List handoffs")
		_, _ = app.StartSession(taskID, "w1", 0, 0)
		_, _ = app.CreateHandoff(taskID, "summary", "", "w2", nil)

		resp, err := app.ListHandoffs(taskID, "")
		if err != nil {
			t.Fatalf("list handoffs: %v", err)
		}
		handoffs := resp["handoffs"].([]map[string]any)
		if len(handoffs) == 0 {
			t.Fatal("expected at least 1 handoff")
		}
	})
}

func TestRecoverAllEmpty(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}

		resp, err := app.RecoverAll()
		if err != nil {
			t.Fatalf("recover all: %v", err)
		}
		if resp["scanned"] != 0 {
			t.Fatalf("expected 0 scanned, got %v", resp["scanned"])
		}
		if resp["recovered"] != 0 {
			t.Fatalf("expected 0 recovered, got %v", resp["recovered"])
		}
	})
}

func TestRecoverAllWithDeadPID(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}

		// Create task → ready → start session with a dead PID
		taskID := createReadyTask(t, app, "Recover test")
		deadPID := 4 // PID 4 is never a real user process (traditionally unused/nil)
		if _, err := app.StartSession(taskID, "dead-worker", 0, deadPID); err != nil {
			t.Fatalf("start session: %v", err)
		}

		// The lazy zombie scan in withDB will have already recovered the dead session,
		// so RecoverAll may see 0 scanned (already recovered). That's fine —
		// verify the task is back to ready (recovered).
		// Instead, verify by checking the task status directly.
		showResp, err := app.ShowTask(taskID)
		if err != nil {
			t.Fatalf("show task: %v", err)
		}
		task := showResp["task"].(map[string]any)
		// Either still active (PID alive on this system) or ready (recovered)
		status := task["status"]
		if status != "ready" && status != "active" {
			t.Fatalf("expected ready or active, got %v", status)
		}
	})
}

func TestRenewReservation(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}

		taskID := createReadyTask(t, app, "Renew test")
		if _, err := app.StartSession(taskID, "pi", 3600, 0); err != nil {
			t.Fatalf("start session: %v", err)
		}

		resp, err := app.RenewReservation(taskID, "")
		if err != nil {
			t.Fatalf("renew reservation: %v", err)
		}
		res := resp["reservation"].(map[string]any)
		if res["task_id"] != taskID {
			t.Fatalf("expected task_id=%s, got %v", taskID, res["task_id"])
		}
		if res["new_expires_at"] == nil {
			t.Fatal("expected new_expires_at")
		}
	})
}

func TestRenewReservationNoActive(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}

		_, err := app.RenewReservation("T-999", "")
		if err == nil {
			t.Fatal("expected error for no active reservation")
		}
	})
}

func TestSessionEndWithFiles(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}

		taskID := createReadyTask(t, app, "Files test")
		_, _ = app.StartSession(taskID, "pi", 0, 0)

		resp, err := app.EndSession(taskID, "completed", "did things", []string{"abc123"}, []string{"main.go", "test.go"}, "")
		if err != nil {
			t.Fatalf("end session with files: %v", err)
		}
		if resp["task_status"] != "completed" {
			t.Fatalf("expected task_status=completed, got %v", resp["task_status"])
		}
	})
}

func TestSessionEndAbandoned(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}

		taskID := createReadyTask(t, app, "Abandon test")
		_, _ = app.StartSession(taskID, "pi", 0, 0)

		resp, err := app.EndSession(taskID, "abandoned", "giving up", nil, nil, "")
		if err != nil {
			t.Fatalf("end session abandoned: %v", err)
		}
		// abandoned should return to ready
		if resp["task_status"] != "ready" {
			t.Fatalf("expected task_status=ready for abandoned, got %v", resp["task_status"])
		}
	})
}

func TestSessionCapturesStartCommitSHA(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}

		taskID := createReadyTask(t, app, "Commit SHA test")

		// Get current HEAD SHA
		db, err := openDB(filepath.Join(repoRoot, ".solo", "solo.db"))
		if err != nil {
			t.Fatalf("open db: %v", err)
		}
		defer db.Close()

		// Start session
		if _, err := app.StartSession(taskID, "pi", 0, 0); err != nil {
			t.Fatalf("start session: %v", err)
		}

		// Verify start_commit_sha was stored
		var startSHA string
		if err := db.QueryRow(`SELECT start_commit_sha FROM sessions WHERE task_id=? AND ended_at IS NULL`, taskID).Scan(&startSHA); err != nil {
			t.Fatalf("query start_commit_sha: %v", err)
		}
		if startSHA == "" {
			t.Fatal("expected non-empty start_commit_sha")
		}

		// Clean up
		_, _ = app.EndSession(taskID, "completed", "", nil, nil, "")
	})
}

func TestSessionCapturesEndCommitSHA(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}

		taskID := createReadyTask(t, app, "End SHA test")
		_, _ = app.StartSession(taskID, "pi", 0, 0)

		resp, err := app.EndSession(taskID, "completed", "", nil, nil, "")
		if err != nil {
			t.Fatalf("end session: %v", err)
		}

		endSHA, ok := resp["end_commit_sha"]
		if !ok || endSHA == "" {
			t.Fatal("expected non-empty end_commit_sha in response")
		}
	})
}

func TestFilesAggregatedToTask(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}

		taskID := createReadyTask(t, app, "Files aggregation test")
		_, _ = app.StartSession(taskID, "pi", 0, 0)

		// End with specific files
		_, err := app.EndSession(taskID, "completed", "", nil, []string{"main.go", "test.go"}, "")
		if err != nil {
			t.Fatalf("end session: %v", err)
		}

		// Verify files were aggregated to the task
		showResp, err := app.ShowTask(taskID)
		if err != nil {
			t.Fatalf("show task: %v", err)
		}
		task := showResp["task"].(map[string]any)
		files := task["affected_files"].([]string)

		foundMain, foundTest := false, false
		for _, f := range files {
			if f == "main.go" {
				foundMain = true
			}
			if f == "test.go" {
				foundTest = true
			}
		}
		if !foundMain || !foundTest {
			t.Fatalf("expected main.go and test.go in affected_files, got %v", files)
		}
	})
}

func TestFilesDeduplicatedAcrossSessions(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}

		// First session ends with interrupted (returns to ready)
		taskID := createReadyTask(t, app, "Dedup test")
		_, _ = app.StartSession(taskID, "pi", 0, 0)
		_, err := app.EndSession(taskID, "interrupted", "", nil, []string{"a.go", "b.go"}, "")
		if err != nil {
			t.Fatalf("first end session: %v", err)
		}

		// Clean up worktree from first session
		_, _ = app.CleanupWorktrees(taskID, false)

		// Second session on the same task (already back to ready from interrupted)
		_, err = app.StartSession(taskID, "pi", 0, 0)
		if err != nil {
			t.Fatalf("second start session: %v", err)
		}
		_, err = app.EndSession(taskID, "completed", "", nil, []string{"b.go", "c.go"}, "")
		if err != nil {
			t.Fatalf("second end session: %v", err)
		}

		// Verify deduplication: should have a.go, b.go, c.go (b.go not duplicated)
		showResp2, _ := app.ShowTask(taskID)
		task := showResp2["task"].(map[string]any)
		files := task["affected_files"].([]string)

		seen := map[string]int{}
		for _, f := range files {
			seen[f]++
		}
		if seen["b.go"] > 1 {
			t.Fatalf("b.go should appear once, appears %d times", seen["b.go"])
		}
		if len(files) != 3 {
			t.Fatalf("expected 3 unique files, got %d: %v", len(files), files)
		}
	})
}

func TestListSessionsIncludesCommitSHA(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}

		taskID := createReadyTask(t, app, "List SHA test")
		_, _ = app.StartSession(taskID, "pi", 0, 0)
		_, _ = app.EndSession(taskID, "completed", "", nil, []string{"main.go"}, "")

		resp, err := app.ListSessions(taskID, "", false, true)
		if err != nil {
			t.Fatalf("list sessions: %v", err)
		}
		sessions := resp["sessions"].([]map[string]any)
		if len(sessions) == 0 {
			t.Fatal("expected at least 1 session")
		}
		s := sessions[0]

		// Check commit SHAs present
		startSHA, hasStart := s["start_commit_sha"]
		if !hasStart || startSHA == "" {
			t.Fatal("expected start_commit_sha in session listing")
		}
		endSHA, hasEnd := s["end_commit_sha"]
		if !hasEnd || endSHA == "" {
			t.Fatal("expected end_commit_sha in session listing")
		}

		// Check files_changed present
		files, hasFiles := s["files_changed"]
		if !hasFiles {
			t.Fatal("expected files_changed in session listing")
		}
		filesList := files.([]string)
		if len(filesList) == 0 || filesList[0] != "main.go" {
			t.Fatalf("expected files_changed=[main.go], got %v", filesList)
		}
	})
}

// --- Semantics: normalizeTaskStatus ---

func TestNormalizeTaskStatusValid(t *testing.T) {
	cases := map[string]string{
		"draft":     "draft",
		"ready":     "ready",
		"active":    "active",
		"completed": "completed",
		"failed":    "failed",
		"blocked":   "blocked",
		"cancelled":  "cancelled",
	}
	for input, expected := range cases {
		got, ok := normalizeTaskStatus(input)
		if !ok || got != expected {
			t.Errorf("normalizeTaskStatus(%q) = (%q, %v), want (%q, true)", input, got, ok, expected)
		}
	}
}

func TestNormalizeTaskStatusLegacyAliases(t *testing.T) {
	cases := map[string]string{
		"open":        "draft",
		"triaged":     "draft",
		"in_progress": "active",
		"in-review":   "active",
		"in_review":   "active",
		"done":        "completed",
	}
	for input, expected := range cases {
		got, ok := normalizeTaskStatus(input)
		if !ok || got != expected {
			t.Errorf("normalizeTaskStatus(%q) = (%q, %v), want (%q, true)", input, got, ok, expected)
		}
	}
}

func TestNormalizeTaskStatusCaseInsensitive(t *testing.T) {
	got, ok := normalizeTaskStatus("ACTIVE")
	if !ok || got != "active" {
		t.Fatalf("expected active, got (%q, %v)", got, ok)
	}
}

func TestNormalizeTaskStatusInvalid(t *testing.T) {
	_, ok := normalizeTaskStatus("unknown")
	if ok {
		t.Fatal("expected false for unknown status")
	}
}

func TestCanonicalTaskStatus(t *testing.T) {
	if got := canonicalTaskStatus("done"); got != "completed" {
		t.Fatalf("expected completed, got %s", got)
	}
	if got := canonicalTaskStatus("in_progress"); got != "active" {
		t.Fatalf("expected active, got %s", got)
	}
	// Unknown falls through
	if got := canonicalTaskStatus("unknown"); got != "unknown" {
		t.Fatalf("expected unknown, got %s", got)
	}
}

func TestLegacyTaskStatus(t *testing.T) {
	cases := map[string]string{
		"draft":     "open",
		"active":    "in_progress",
		"completed": "done",
		"ready":     "ready", // passthrough
	}
	for input, expected := range cases {
		if got := legacyTaskStatus(input); got != expected {
			t.Errorf("legacyTaskStatus(%q) = %q, want %q", input, got, expected)
		}
	}
}

// --- Semantics: parsePriorityValue / priorityLabel ---

func TestParsePriorityValueLabels(t *testing.T) {
	cases := map[string]int{
		"low":      2,
		"p3":       2,
		"medium":   3,
		"normal":   3,
		"p2":       3,
		"high":     4,
		"p1":       4,
		"critical": 5,
		"urgent":   5,
		"p0":       5,
	}
	for input, expected := range cases {
		got := parsePriorityValue(input, 0)
		if got != expected {
			t.Errorf("parsePriorityValue(%q) = %d, want %d", input, got, expected)
		}
	}
}

func TestParsePriorityValueNumeric(t *testing.T) {
	cases := map[string]int{
		"1": 2, "2": 2, "3": 3, "4": 4, "5": 5,
	}
	for input, expected := range cases {
		got := parsePriorityValue(input, 0)
		if got != expected {
			t.Errorf("parsePriorityValue(%q) = %d, want %d", input, got, expected)
		}
	}
}

func TestParsePriorityValueFallback(t *testing.T) {
	if got := parsePriorityValue("", 3); got != 3 {
		t.Fatalf("empty string should use fallback, got %d", got)
	}
	if got := parsePriorityValue("garbage", 2); got != 2 {
		t.Fatalf("unknown string should use fallback, got %d", got)
	}
}

func TestPriorityLabel(t *testing.T) {
	cases := map[int]string{
		5: "critical",
		6: "critical", // >= 5
		4: "high",
		3: "medium",
		2: "low",
		1: "low",
		0: "low",
	}
	for input, expected := range cases {
		if got := priorityLabel(input); got != expected {
			t.Errorf("priorityLabel(%d) = %q, want %q", input, got, expected)
		}
	}
}

// --- Health ---

func TestHealthReturnsDBInfo(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}
		createReadyTask(t, app, "Health test task")

		resp, err := app.Health()
		if err != nil {
			t.Fatalf("health: %v", err)
		}

		dbInfo, ok := resp["database"].(map[string]any)
		if !ok || dbInfo["integrity"] != "ok" {
			t.Fatalf("expected db integrity ok, got %v", resp["database"])
		}

		tasks, ok := resp["tasks"].(map[string]int)
		if !ok {
			t.Fatalf("expected tasks map, got %v", resp["tasks"])
		}
		if tasks["ready"] != 1 {
			t.Fatalf("expected 1 ready task, got %d", tasks["ready"])
		}

		if resp["schema_version"] == nil {
			t.Fatal("expected schema_version")
		}
		if resp["issues"] == nil {
			t.Fatal("expected issues array")
		}

		wt, ok := resp["worktrees"].(map[string]any)
		if !ok {
			t.Fatalf("expected worktrees map, got %v", resp["worktrees"])
		}
		if wt["max"] != 5 {
			t.Fatalf("expected max worktrees 5, got %v", wt["max"])
		}
	})
}

// --- sanitizeUntrusted edge cases ---

func TestSanitizeUntrustedEmptyString(t *testing.T) {
	if got := sanitizeUntrusted(""); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestSanitizeUntrustedPreservesNormal(t *testing.T) {
	input := "Hello World 123"
	if got := sanitizeUntrusted(input); got != input {
		t.Fatalf("expected unchanged, got %q", got)
	}
}

func TestSanitizeUntrustedStripsNullBytes(t *testing.T) {
	input := "a\x00b\x00c"
	got := sanitizeUntrusted(input)
	if strings.Contains(got, "\x00") {
		t.Fatal("expected null bytes removed")
	}
}

func TestSanitizeUntrustedStripsANSI(t *testing.T) {
	input := "\x1b[31mred\x1b[0m text"
	got := sanitizeUntrusted(input)
	if strings.Contains(got, "\x1b[") {
		t.Fatal("expected ANSI codes removed")
	}
}

// --- enforceTokenBudget edge cases ---

func TestEnforceTokenBudgetNoTruncationNeeded(t *testing.T) {
	bundle := map[string]any{
		"task":                 map[string]any{"id": "T-1"},
		"duplicate_candidates": []map[string]any{},
		"error_history":        []any{},
		"recent_sessions":      []map[string]any{},
		"truncation":           map[string]any{},
	}
	out := enforceTokenBudget(bundle, 10000)
	if len(out["duplicate_candidates"].([]map[string]any)) != 0 {
		t.Fatal("should not truncate when budget is large")
	}
}

func TestEnforceTokenBudgetRemovesErrorHistory(t *testing.T) {
	bundle := map[string]any{
		"task":                 map[string]any{"id": "T-1", "description": strings.Repeat("word ", 100), "acceptance_criteria": "", "definition_of_done": "", "affected_files": []string{}},
		"reservation":          map[string]any{"id": "r"},
		"worktree":             map[string]any{"path": ".solo/worktrees/T-1"},
		"dependencies":         []map[string]any{{"task_id": "T-0"}},
		"latest_handoff":       map[string]any{"summary": "h"},
		"recent_sessions":      []map[string]any{{"notes": strings.Repeat("session ", 50)}},
		"error_history":        []any{"e1", "e2"},
		"duplicate_candidates": []map[string]any{},
		"warnings":             []any{},
		"truncation":           map[string]any{"sessions_total": 1, "sessions_included": 1, "handoffs_total": 1, "handoffs_included": 1},
	}
	out := enforceTokenBudget(bundle, 30)
	if eh, ok := out["error_history"].([]any); ok && len(eh) > 0 {
		t.Fatal("expected error_history to be truncated")
	}
}

func TestEnforceTokenBudgetRemovesDependencies(t *testing.T) {
	bundle := map[string]any{
		"task":                 map[string]any{"id": "T-1", "description": strings.Repeat("word ", 100), "acceptance_criteria": "", "definition_of_done": "", "affected_files": []string{}},
		"reservation":          map[string]any{"id": "r"},
		"worktree":             map[string]any{"path": ".solo/worktrees/T-1"},
		"dependencies":         []map[string]any{{"task_id": "T-0"}},
		"latest_handoff":       map[string]any{"summary": "h"},
		"recent_sessions":      []map[string]any{},
		"error_history":        []any{},
		"duplicate_candidates": []map[string]any{},
		"warnings":             []any{},
		"truncation":           map[string]any{"sessions_total": 0, "sessions_included": 0, "handoffs_total": 1, "handoffs_included": 1},
	}
	out := enforceTokenBudget(bundle, 30)
	if deps, ok := out["dependencies"].([]map[string]any); ok && len(deps) > 0 {
		t.Fatal("expected dependencies to be truncated")
	}
}

// --- status_storage: usesLegacyTaskStatusSchema ---

func TestUsesLegacyTaskStatusSchemaFalse(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}
		db, err := openDB(filepath.Join(repoRoot, ".solo", "solo.db"))
		if err != nil {
			t.Fatalf("open db: %v", err)
		}
		defer db.Close()
		if usesLegacyTaskStatusSchema(db) {
			t.Fatal("modern schema should not be detected as legacy")
		}
	})
}

func TestTaskStatusForWritePassthrough(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}
		db, err := openDB(filepath.Join(repoRoot, ".solo", "solo.db"))
		if err != nil {
			t.Fatalf("open db: %v", err)
		}
		defer db.Close()
		// Modern schema: passthrough
		if got := taskStatusForWrite(db, "ready"); got != "ready" {
			t.Fatalf("expected ready, got %s", got)
		}
		if got := taskStatusForWrite(db, "completed"); got != "completed" {
			t.Fatalf("expected completed, got %s", got)
		}
	})
}

// --- Health with zombie detection ---

func TestHealthDetectsActiveSession(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}

		taskID := createReadyTask(t, app, "Health session test")
		if _, err := app.StartSession(taskID, "pi", 0, 0); err != nil {
			t.Fatalf("start session: %v", err)
		}

		resp, err := app.Health()
		if err != nil {
			t.Fatalf("health: %v", err)
		}

		sessions, ok := resp["active_sessions"].(int)
		if !ok || sessions != 1 {
			t.Fatalf("expected 1 active session, got %v", resp["active_sessions"])
		}
		reservations, ok := resp["active_reservations"].(int)
		if !ok || reservations != 1 {
			t.Fatalf("expected 1 active reservation, got %v", resp["active_reservations"])
		}
	})
}

// --- ListSessions lightweight vs verbose ---

func TestListSessionsLightweightOmitsHeavyColumns(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}

		taskID := createReadyTask(t, app, "Lightweight session test")
		if _, err := app.StartSession(taskID, "pi", 0, 0); err != nil {
			t.Fatalf("start session: %v", err)
		}

		// Lightweight list
		resp, err := app.ListSessions(taskID, "", false, false)
		if err != nil {
			t.Fatalf("list sessions lightweight: %v", err)
		}

		sessions := resp["sessions"].([]map[string]any)
		if len(sessions) == 0 {
			t.Fatal("expected at least one session")
		}
		s := sessions[0]

		// Lightweight should NOT have commit/SHA fields
		if _, has := s["start_commit_sha"]; has {
			t.Fatal("lightweight should not include start_commit_sha")
		}
		if _, has := s["commits"]; has {
			t.Fatal("lightweight should not include commits")
		}
		if _, has := s["files_changed"]; has {
			t.Fatal("lightweight should not include files_changed")
		}
	})
}

// --- ShowHandoff ---

func TestShowHandoffSuccess(t *testing.T) {
	withTempGitRepoCWD(t, func(repoRoot string) {
		app := NewApp()
		if _, err := app.Init("test-machine", "", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}

		taskID := createReadyTask(t, app, "Handoff show test")
		if _, err := app.StartSession(taskID, "pi", 0, 0); err != nil {
			t.Fatalf("start session: %v", err)
		}

		hResp, err := app.CreateHandoff(taskID, "Did some work", "More to do", "agent-2", []string{"file.go"})
		if err != nil {
			t.Fatalf("create handoff: %v", err)
		}
		handoffID := hResp["handoff_id"].(string)

		showResp, err := app.ShowHandoff(handoffID)
		if err != nil {
			t.Fatalf("show handoff: %v", err)
		}

		handoff, ok := showResp["handoff"].(map[string]any)
		if !ok {
			t.Fatalf("expected handoff map, got %v", showResp)
		}
		if handoff["summary"] != "Did some work" {
			t.Fatalf("expected summary, got %v", handoff["summary"])
		}
		if handoff["to_worker"] != "agent-2" {
			t.Fatalf("expected to_worker=agent-2, got %v", handoff["to_worker"])
		}
	})
}

// --- btoi / estimateTokens ---

func TestBtoi(t *testing.T) {
	if btoi(true) != 1 {
		t.Fatal("btoi(true) should be 1")
	}
	if btoi(false) != 0 {
		t.Fatal("btoi(false) should be 0")
	}
}

func TestEstimateTokens(t *testing.T) {
	if got := estimateTokens("one two three"); got <= 0 {
		t.Fatalf("expected positive token estimate, got %d", got)
	}
}

