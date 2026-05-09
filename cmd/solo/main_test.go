package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"solo/internal/solo"
)

// captureOutput runs fn while capturing stdout, returns the captured output.
func captureOutput(fn func()) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = old
	buf, _ := io.ReadAll(r)
	return string(buf)
}

// withSetup creates a temp git repo, inits solo, and calls fn with the app.
func withSetup(t *testing.T, fn func(app *solo.App, repoRoot string)) {
	t.Helper()
	tmp := t.TempDir()
	mustGit(t, tmp, "init", "-b", "main")
	mustGit(t, tmp, "config", "user.email", "test@example.com")
	mustGit(t, tmp, "config", "user.name", "Test")
	_ = os.WriteFile(filepath.Join(tmp, "README.md"), []byte("init\n"), 0o644)
	mustGit(t, tmp, "add", "README.md")
	mustGit(t, tmp, "commit", "-m", "init")
	mustGit(t, tmp, "update-ref", "refs/remotes/origin/main", "HEAD")

	old, _ := os.Getwd()
	_ = os.Chdir(tmp)
	defer os.Chdir(old)

	app := solo.NewApp()
	if _, err := app.Init("", "environment", "", false); err != nil {
		t.Fatalf("init: %v", err)
	}
	fn(app, tmp)
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// parseResponse parses a JSON response into a map.
func parseResponse(t *testing.T, raw string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("parse json: %v\nraw: %q", err, raw)
	}
	return m
}

// runCapture calls run() and returns (stdout output, error).
// For error paths, it uses the same writeErrorEnvelope as main().
func runCapture(app *solo.App, args []string) (string, error) {
	var out string
	var err error
	out = captureOutput(func() {
		err = run(app, args)
	})
	// If run() returned an error, reuse the same envelope logic as main()
	if err != nil {
		out = captureOutput(func() {
			writeErrorEnvelope(err)
		})
	}
	return out, err
}

// toString converts an int to string for CLI args.
func toString(v int) string {
	return fmt.Sprintf("%d", v)
}

// --- Error path tests ---

func TestRunMissingCommand(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		out, err := runCapture(app, []string{})
		if err == nil {
			t.Fatal("expected error for missing command")
		}
		resp := parseResponse(t, out)
		if resp["ok"] != false {
			t.Fatalf("expected ok=false, got %v", resp["ok"])
		}
		errObj := resp["error"].(map[string]any)
		if errObj["code"] != "INVALID_ARGUMENT" {
			t.Fatalf("expected INVALID_ARGUMENT, got %v", errObj["code"])
		}
	})
}

func TestRunUnknownCommand(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		out, err := runCapture(app, []string{"bogus"})
		if err == nil {
			t.Fatal("expected error for unknown command")
		}
		resp := parseResponse(t, out)
		if resp["ok"] != false {
			t.Fatalf("expected ok=false, got %v", resp["ok"])
		}
	})
}

// --- Happy path tests ---

func TestRunHelp(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		out, err := runCapture(app, []string{"help"})
		if err != nil {
			t.Fatalf("help: %v", err)
		}
		resp := parseResponse(t, out)
		if resp["ok"] != true {
			t.Fatalf("expected ok=true, got %v", resp["ok"])
		}
		cmds, ok := resp["data"].(map[string]any)["commands"]
		if !ok || cmds == nil {
			t.Fatal("expected commands in help output")
		}
	})
}

func TestRunHelpFlag(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		out, err := runCapture(app, []string{"--help"})
		if err != nil {
			t.Fatalf("help: %v", err)
		}
		resp := parseResponse(t, out)
		if resp["ok"] != true {
			t.Fatalf("expected ok=true, got %v", resp["ok"])
		}
	})
}

func TestHealthCommand(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		out, err := runCapture(app, []string{"health"})
		if err != nil {
			t.Fatalf("health: %v", err)
		}
		resp := parseResponse(t, out)
		if resp["ok"] != true {
			t.Fatalf("expected ok=true, got %v", resp["ok"])
		}
		data := resp["data"].(map[string]any)
		db := data["database"].(map[string]any)
		if db["integrity"] != "ok" {
			t.Fatalf("expected db integrity=ok, got %v", db["integrity"])
		}
	})
}

func TestTaskCreateAndShow(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		out, err := runCapture(app, []string{"task", "create", "--title", "Test task", "--priority", "high", "--labels", "bug,ui"})
		if err != nil {
			t.Fatalf("task create: %v", err)
		}
		resp := parseResponse(t, out)
		task := resp["data"].(map[string]any)["task"].(map[string]any)
		taskID := task["id"].(string)
		if task["title"] != "Test task" {
			t.Fatalf("expected title='Test task', got %v", task["title"])
		}
		if task["priority"] != "high" {
			t.Fatalf("expected priority=high, got %v", task["priority"])
		}

		// Show the task
		out2, err2 := runCapture(app, []string{"task", "show", taskID})
		if err2 != nil {
			t.Fatalf("task show: %v", err2)
		}
		resp2 := parseResponse(t, out2)
		shown := resp2["data"].(map[string]any)["task"].(map[string]any)
		if shown["id"] != taskID {
			t.Fatalf("expected id=%s, got %v", taskID, shown["id"])
		}
	})
}

func TestTaskList(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		_, _ = runCapture(app, []string{"task", "create", "--title", "Task A"})
		_, _ = runCapture(app, []string{"task", "create", "--title", "Task B"})

		out, err := runCapture(app, []string{"task", "list"})
		if err != nil {
			t.Fatalf("task list: %v", err)
		}
		resp := parseResponse(t, out)
		data := resp["data"].(map[string]any)
		tasks := data["tasks"].([]any)
		if len(tasks) < 2 {
			t.Fatalf("expected at least 2 tasks, got %d", len(tasks))
		}
	})
}

func TestTaskListByStatus(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		_, _ = runCapture(app, []string{"task", "create", "--title", "Filtered"})
		out, err := runCapture(app, []string{"task", "list", "--status", "draft"})
		if err != nil {
			t.Fatalf("task list --status: %v", err)
		}
		resp := parseResponse(t, out)
		tasks := resp["data"].(map[string]any)["tasks"].([]any)
		if len(tasks) == 0 {
			t.Fatal("expected at least 1 draft task")
		}
	})
}

func TestTaskUpdate(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		out, _ := runCapture(app, []string{"task", "create", "--title", "Original"})
		resp := parseResponse(t, out)
		task := resp["data"].(map[string]any)["task"].(map[string]any)
		taskID := task["id"].(string)
		version := int(task["version"].(float64))

		out2, err := runCapture(app, []string{"task", "update", taskID, "--title", "Updated", "--version", toString(version)})
		if err != nil {
			t.Fatalf("task update: %v", err)
		}
		resp2 := parseResponse(t, out2)
		updated := resp2["data"].(map[string]any)["task"].(map[string]any)
		if updated["title"] != "Updated" {
			t.Fatalf("expected title=Updated, got %v", updated["title"])
		}
	})
}

func TestTaskReady(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		out, _ := runCapture(app, []string{"task", "create", "--title", "Ready test"})
		resp := parseResponse(t, out)
		task := resp["data"].(map[string]any)["task"].(map[string]any)
		taskID := task["id"].(string)
		version := int(task["version"].(float64))

		out2, err := runCapture(app, []string{"task", "ready", taskID, "--version", toString(version)})
		if err != nil {
			t.Fatalf("task ready: %v", err)
		}
		resp2 := parseResponse(t, out2)
		ready := resp2["data"].(map[string]any)["task"].(map[string]any)
		if ready["status"] != "ready" {
			t.Fatalf("expected status=ready, got %v", ready["status"])
		}
	})
}

func TestTaskStatusTransition(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		// Create → ready → active (via session) → completed (via session end)
		out, _ := runCapture(app, []string{"task", "create", "--title", "Lifecycle"})
		resp := parseResponse(t, out)
		task := resp["data"].(map[string]any)["task"].(map[string]any)
		taskID := task["id"].(string)
		version := int(task["version"].(float64))

		_, _ = runCapture(app, []string{"task", "ready", taskID, "--version", toString(version)})
		_, _ = runCapture(app, []string{"session", "start", taskID, "--worker", "pi"})
		out3, _ := runCapture(app, []string{"session", "end", taskID, "--result", "completed"})

		resp3 := parseResponse(t, out3)
		if resp3["ok"] != true {
			t.Fatalf("expected ok=true, got %v", resp3)
		}
	})
}

func TestSessionStartAndEnd(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		// Create and ready a task
		out, _ := runCapture(app, []string{"task", "create", "--title", "Session test"})
		resp := parseResponse(t, out)
		task := resp["data"].(map[string]any)["task"].(map[string]any)
		taskID := task["id"].(string)
		version := int(task["version"].(float64))
		_, _ = runCapture(app, []string{"task", "ready", taskID, "--version", toString(version)})

		// Start session without PID
		out2, err := runCapture(app, []string{"session", "start", taskID, "--worker", "test-worker"})
		if err != nil {
			t.Fatalf("session start: %v", err)
		}
		resp2 := parseResponse(t, out2)
		if resp2["ok"] != true {
			t.Fatalf("expected ok=true, got %v", resp2)
		}

		// End session
		out3, err2 := runCapture(app, []string{"session", "end", taskID, "--result", "completed"})
		if err2 != nil {
			t.Fatalf("session end: %v", err2)
		}
		resp3 := parseResponse(t, out3)
		if resp3["ok"] != true {
			t.Fatalf("expected ok=true, got %v", resp3)
		}
	})
}

func TestSessionStartWithPID(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		out, _ := runCapture(app, []string{"task", "create", "--title", "PID test"})
		resp := parseResponse(t, out)
		task := resp["data"].(map[string]any)["task"].(map[string]any)
		taskID := task["id"].(string)
		version := int(task["version"].(float64))
		_, _ = runCapture(app, []string{"task", "ready", taskID, "--version", toString(version)})

		out2, err := runCapture(app, []string{"session", "start", taskID, "--worker", "pi", "--pid", toString(os.Getpid())})
		if err != nil {
			t.Fatalf("session start --pid: %v", err)
		}
		resp2 := parseResponse(t, out2)
		if resp2["ok"] != true {
			t.Fatalf("expected ok=true, got %v", resp2)
		}
		// Clean up
		_, _ = runCapture(app, []string{"session", "end", taskID, "--result", "completed"})
	})
}

func TestSessionList(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		out, err := runCapture(app, []string{"session", "list"})
		if err != nil {
			t.Fatalf("session list: %v", err)
		}
		resp := parseResponse(t, out)
		if resp["ok"] != true {
			t.Fatalf("expected ok=true, got %v", resp)
		}
	})
}

func TestSessionListActive(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		out, err := runCapture(app, []string{"session", "list", "--active"})
		if err != nil {
			t.Fatalf("session list --active: %v", err)
		}
		resp := parseResponse(t, out)
		if resp["ok"] != true {
			t.Fatalf("expected ok=true, got %v", resp)
		}
	})
}

func TestHandoffCreateAndList(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		// Create task, make ready, start session
		out, _ := runCapture(app, []string{"task", "create", "--title", "Handoff test"})
		resp := parseResponse(t, out)
		task := resp["data"].(map[string]any)["task"].(map[string]any)
		taskID := task["id"].(string)
		version := int(task["version"].(float64))
		_, _ = runCapture(app, []string{"task", "ready", taskID, "--version", toString(version)})
		_, _ = runCapture(app, []string{"session", "start", taskID, "--worker", "agent-a"})

		// Create handoff while session is active
		out2, err := runCapture(app, []string{"handoff", "create", taskID, "--summary", "did stuff", "--to", "agent-b"})
		if err != nil {
			t.Fatalf("handoff create: %v", err)
		}
		resp2 := parseResponse(t, out2)
		if resp2["ok"] != true {
			t.Fatalf("expected ok=true, got %v", resp2)
		}

		// End session
		_, _ = runCapture(app, []string{"session", "end", taskID, "--result", "completed"})

		// List handoffs
		out3, err2 := runCapture(app, []string{"handoff", "list", "--task", taskID})
		if err2 != nil {
			t.Fatalf("handoff list: %v", err2)
		}
		resp3 := parseResponse(t, out3)
		handoffs := resp3["data"].(map[string]any)["handoffs"].([]any)
		if len(handoffs) == 0 {
			t.Fatal("expected at least 1 handoff")
		}
	})
}

func TestWorktreeList(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		out, err := runCapture(app, []string{"worktree", "list"})
		if err != nil {
			t.Fatalf("worktree list: %v", err)
		}
		resp := parseResponse(t, out)
		if resp["ok"] != true {
			t.Fatalf("expected ok=true, got %v", resp)
		}
	})
}

func TestWorktreeCleanup(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		// Create task → ready → session start → session end → cleanup
		out, _ := runCapture(app, []string{"task", "create", "--title", "Cleanup test"})
		resp := parseResponse(t, out)
		task := resp["data"].(map[string]any)["task"].(map[string]any)
		taskID := task["id"].(string)
		version := int(task["version"].(float64))
		_, _ = runCapture(app, []string{"task", "ready", taskID, "--version", toString(version)})
		_, _ = runCapture(app, []string{"session", "start", taskID, "--worker", "pi"})
		_, _ = runCapture(app, []string{"session", "end", taskID, "--result", "completed"})

		out2, err := runCapture(app, []string{"worktree", "cleanup", taskID})
		if err != nil {
			t.Fatalf("worktree cleanup: %v", err)
		}
		resp2 := parseResponse(t, out2)
		if resp2["ok"] != true {
			t.Fatalf("expected ok=true, got %v", resp2)
		}
	})
}

func TestAuditList(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		// Generate some audit events
		_, _ = runCapture(app, []string{"task", "create", "--title", "Audit test"})

		out, err := runCapture(app, []string{"audit", "list"})
		if err != nil {
			t.Fatalf("audit list: %v", err)
		}
		resp := parseResponse(t, out)
		if resp["ok"] != true {
			t.Fatalf("expected ok=true, got %v", resp)
		}
		events := resp["data"].(map[string]any)["events"].([]any)
		if len(events) == 0 {
			t.Fatal("expected at least 1 audit event")
		}
	})
}

func TestAuditListWithTaskFilter(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		out, _ := runCapture(app, []string{"task", "create", "--title", "Filter test"})
		resp := parseResponse(t, out)
		taskID := resp["data"].(map[string]any)["task"].(map[string]any)["id"].(string)

		out2, err := runCapture(app, []string{"audit", "list", "--task", taskID, "--limit", "5"})
		if err != nil {
			t.Fatalf("audit list --task: %v", err)
		}
		resp2 := parseResponse(t, out2)
		if resp2["ok"] != true {
			t.Fatalf("expected ok=true, got %v", resp2)
		}
	})
}

func TestAuditShow(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		_, _ = runCapture(app, []string{"task", "create", "--title", "Audit show test"})

		// Get event ID from list
		out, _ := runCapture(app, []string{"audit", "list"})
		resp := parseResponse(t, out)
		events := resp["data"].(map[string]any)["events"].([]any)
		if len(events) == 0 {
			t.Fatal("no audit events")
		}
		event := events[0].(map[string]any)
		eventID := int(event["id"].(float64))

		out2, err := runCapture(app, []string{"audit", "show", toString(eventID)})
		if err != nil {
			t.Fatalf("audit show: %v", err)
		}
		resp2 := parseResponse(t, out2)
		if resp2["ok"] != true {
			t.Fatalf("expected ok=true, got %v", resp2)
		}
	})
}

func TestRecoverAll(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		out, err := runCapture(app, []string{"recover", "--all"})
		if err != nil {
			t.Fatalf("recover --all: %v", err)
		}
		resp := parseResponse(t, out)
		if resp["ok"] != true {
			t.Fatalf("expected ok=true, got %v", resp)
		}
	})
}

func TestSearchCommand(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		_, _ = runCapture(app, []string{"task", "create", "--title", "Searchable task"})

		out, err := runCapture(app, []string{"search", "Searchable"})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		resp := parseResponse(t, out)
		if resp["ok"] != true {
			t.Fatalf("expected ok=true, got %v", resp)
		}
		results := resp["data"].(map[string]any)["results"].([]any)
		if len(results) == 0 {
			t.Fatal("expected search results")
		}
	})
}

func TestTaskDeps(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		out, _ := runCapture(app, []string{"task", "create", "--title", "Dep parent"})
		resp := parseResponse(t, out)
		taskID := resp["data"].(map[string]any)["task"].(map[string]any)["id"].(string)

		out2, err := runCapture(app, []string{"task", "deps", taskID})
		if err != nil {
			t.Fatalf("task deps: %v", err)
		}
		resp2 := parseResponse(t, out2)
		if resp2["ok"] != true {
			t.Fatalf("expected ok=true, got %v", resp2)
		}
	})
}

func TestTaskTree(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		out, _ := runCapture(app, []string{"task", "create", "--title", "Tree root"})
		resp := parseResponse(t, out)
		taskID := resp["data"].(map[string]any)["task"].(map[string]any)["id"].(string)

		out2, err := runCapture(app, []string{"task", "tree", taskID})
		if err != nil {
			t.Fatalf("task tree: %v", err)
		}
		resp2 := parseResponse(t, out2)
		if resp2["ok"] != true {
			t.Fatalf("expected ok=true, got %v", resp2)
		}
	})
}

func TestReservationRenewMissingID(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		_, err := runCapture(app, []string{"reservation", "renew"})
		if err == nil {
			t.Fatal("expected error for missing task id")
		}
	})
}

// --- Response envelope tests ---

func TestResponseEnvelopeSuccess(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		out, err := runCapture(app, []string{"task", "create", "--title", "Envelope test"})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		resp := parseResponse(t, out)
		if resp["ok"] != true {
			t.Fatalf("expected ok=true, got %v", resp["ok"])
		}
		if _, has := resp["data"]; !has {
			t.Fatal("expected 'data' key in response envelope")
		}
		if _, has := resp["error"]; has {
			t.Fatal("did not expect 'error' key in success response")
		}
	})
}

func TestResponseEnvelopeError(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		out, err := runCapture(app, []string{"task", "show", "T-999"})
		if err == nil {
			t.Fatal("expected error for nonexistent task")
		}
		resp := parseResponse(t, out)
		if resp["ok"] != false {
			t.Fatalf("expected ok=false, got %v", resp["ok"])
		}
		errObj, has := resp["error"]
		if !has {
			t.Fatal("expected 'error' key in error response")
		}
		errMap := errObj.(map[string]any)
		if errMap["code"] == nil {
			t.Fatal("expected error.code in error envelope")
		}
	})
}

// --- Unit tests for helpers ---

func TestParsePriority(t *testing.T) {
	tests := []struct {
		input    string
		fallback int
		want     int
	}{
		{"low", 0, 2},
		{"medium", 0, 3},
		{"high", 0, 4},
		{"critical", 0, 5},
		{"p0", 0, 5},
		{"p1", 0, 4},
		{"p2", 0, 3},
		{"p3", 0, 2},
		{"2", 0, 2},
		{"5", 0, 5},
		{"unknown", 3, 3},
		{"", 3, 3},
	}
	for _, tt := range tests {
		got := parsePriority(tt.input, tt.fallback)
		if got != tt.want {
			t.Errorf("parsePriority(%q, %d) = %d, want %d", tt.input, tt.fallback, got, tt.want)
		}
	}
}

func TestSplitCSV(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"a,b,c", 3},
		{"a, b , c", 3},
		{"", 0},
		{"  ", 0},
		{"a", 1},
	}
	for _, tt := range tests {
		got := splitCSV(tt.input)
		if len(got) != tt.want {
			t.Errorf("splitCSV(%q) = %d items, want %d", tt.input, len(got), tt.want)
		}
	}
}

func TestHasFlag(t *testing.T) {
	if !hasFlag([]string{"--json", "--verbose"}, "--json") {
		t.Error("expected --json to be found")
	}
	if hasFlag([]string{"--json"}, "--help") {
		t.Error("did not expect --help to be found")
	}
}

func TestVal(t *testing.T) {
	args := []string{"--title", "hello", "--priority", "high"}
	i := 0
	if v := val(args, &i); v != "hello" {
		t.Errorf("expected 'hello', got %q", v)
	}
	i++ // advance past --priority
	if v := val(args, &i); v != "high" {
		t.Errorf("expected 'high', got %q", v)
	}
}

func TestValAtEnd(t *testing.T) {
	args := []string{"--title"}
	i := 0
	if v := val(args, &i); v != "" {
		t.Errorf("expected empty string at end, got %q", v)
	}
}

func TestSessionListVerbose(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		out, err := runCapture(app, []string{"session", "list", "--verbose"})
		if err != nil {
			t.Fatalf("session list --verbose: %v", err)
		}
		resp := parseResponse(t, out)
		if resp["ok"] != true {
			t.Fatalf("expected ok=true, got %v", resp["ok"])
		}
	})
}

func TestHandoffShow(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		// Create task → ready → session → handoff
		out, _ := runCapture(app, []string{"task", "create", "--title", "Handoff show"})
		resp := parseResponse(t, out)
		task := resp["data"].(map[string]any)["task"].(map[string]any)
		taskID := task["id"].(string)
		version := int(task["version"].(float64))
		_, _ = runCapture(app, []string{"task", "ready", taskID, "--version", toString(version)})
		_, _ = runCapture(app, []string{"session", "start", taskID, "--worker", "pi"})

		out2, _ := runCapture(app, []string{"handoff", "create", taskID, "--summary", "did work", "--remaining-work", "more to do", "--to", "agent-b", "--files", "a.go,b.go"})
		resp2 := parseResponse(t, out2)
		handoffID := resp2["data"].(map[string]any)["handoff_id"].(string)

		out3, err := runCapture(app, []string{"handoff", "show", handoffID})
		if err != nil {
			t.Fatalf("handoff show: %v", err)
		}
		resp3 := parseResponse(t, out3)
		handoff := resp3["data"].(map[string]any)["handoff"].(map[string]any)
		if handoff["summary"] != "did work" {
			t.Fatalf("expected summary='did work', got %v", handoff["summary"])
		}
	})
}

func TestWorktreeInspectNotFound(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		_, err := runCapture(app, []string{"worktree", "inspect", "T-999"})
		if err == nil {
			t.Fatal("expected error for missing worktree")
		}
	})
}

func TestTaskContextCommand(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		out, _ := runCapture(app, []string{"task", "create", "--title", "Context test", "--description", "Some description"})
		resp := parseResponse(t, out)
		taskID := resp["data"].(map[string]any)["task"].(map[string]any)["id"].(string)

		out2, err := runCapture(app, []string{"task", "context", taskID})
		if err != nil {
			t.Fatalf("task context: %v", err)
		}
		resp2 := parseResponse(t, out2)
		if resp2["ok"] != true {
			t.Fatalf("expected ok=true, got %v", resp2["ok"])
		}
		data := resp2["data"].(map[string]any)
		if data["task"] == nil {
			t.Fatal("expected task in context output")
		}
	})
}

func TestSearchWithStatus(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		_, _ = runCapture(app, []string{"task", "create", "--title", "Search status test"})

		out, err := runCapture(app, []string{"search", "Search", "--status", "draft", "--limit", "5"})
		if err != nil {
			t.Fatalf("search --status: %v", err)
		}
		resp := parseResponse(t, out)
		if resp["ok"] != true {
			t.Fatalf("expected ok=true, got %v", resp["ok"])
		}
	})
}

func TestTaskRecoverCommand(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		// Create → ready → session start → recover (will end session + set to ready)
		out, _ := runCapture(app, []string{"task", "create", "--title", "Recover test"})
		resp := parseResponse(t, out)
		task := resp["data"].(map[string]any)["task"].(map[string]any)
		taskID := task["id"].(string)
		version := int(task["version"].(float64))
		_, _ = runCapture(app, []string{"task", "ready", taskID, "--version", toString(version)})
		_, _ = runCapture(app, []string{"session", "start", taskID, "--worker", "pi"})

		out2, _ := runCapture(app, []string{"task", "show", taskID})
		resp2 := parseResponse(t, out2)
		v2 := int(resp2["data"].(map[string]any)["task"].(map[string]any)["version"].(float64))

		out3, err := runCapture(app, []string{"task", "recover", taskID, "--version", toString(v2)})
		if err != nil {
			t.Fatalf("task recover: %v", err)
		}
		resp3 := parseResponse(t, out3)
		if resp3["ok"] != true {
			t.Fatalf("expected ok=true, got %v", resp3["ok"])
		}
	})
}

func TestTaskListAvailable(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		_, _ = runCapture(app, []string{"task", "create", "--title", "Available test"})

		out, err := runCapture(app, []string{"task", "list", "--available"})
		if err != nil {
			t.Fatalf("task list --available: %v", err)
		}
		resp := parseResponse(t, out)
		// No tasks should be available since they're in draft
		tasks := resp["data"].(map[string]any)["tasks"].([]any)
		if len(tasks) != 0 {
			t.Fatalf("expected 0 available tasks (all draft), got %d", len(tasks))
		}
	})
}

func TestTaskListWithOffset(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		_, _ = runCapture(app, []string{"task", "create", "--title", "A"})
		_, _ = runCapture(app, []string{"task", "create", "--title", "B"})

		out, err := runCapture(app, []string{"task", "list", "--offset", "1", "--limit", "1"})
		if err != nil {
			t.Fatalf("task list --offset: %v", err)
		}
		resp := parseResponse(t, out)
		tasks := resp["data"].(map[string]any)["tasks"].([]any)
		if len(tasks) != 1 {
			t.Fatalf("expected 1 task with offset, got %d", len(tasks))
		}
	})
}

func TestReservationRenewSuccess(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		out, _ := runCapture(app, []string{"task", "create", "--title", "Renew test"})
		resp := parseResponse(t, out)
		task := resp["data"].(map[string]any)["task"].(map[string]any)
		taskID := task["id"].(string)
		version := int(task["version"].(float64))
		_, _ = runCapture(app, []string{"task", "ready", taskID, "--version", toString(version)})
		_, _ = runCapture(app, []string{"session", "start", taskID, "--worker", "pi"})

		out2, err := runCapture(app, []string{"reservation", "renew", taskID})
		if err != nil {
			t.Fatalf("reservation renew: %v", err)
		}
		resp2 := parseResponse(t, out2)
		if resp2["ok"] != true {
			t.Fatalf("expected ok=true, got %v", resp2["ok"])
		}
	})
}

func TestSessionEndWithFilesAndCommits(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		out, _ := runCapture(app, []string{"task", "create", "--title", "Files test"})
		resp := parseResponse(t, out)
		task := resp["data"].(map[string]any)["task"].(map[string]any)
		taskID := task["id"].(string)
		version := int(task["version"].(float64))
		_, _ = runCapture(app, []string{"task", "ready", taskID, "--version", toString(version)})
		_, _ = runCapture(app, []string{"session", "start", taskID, "--worker", "pi"})

		out2, err := runCapture(app, []string{"session", "end", taskID, "--result", "completed", "--files", "main.go,util.go", "--commits", "abc123", "--notes", "done"})
		if err != nil {
			t.Fatalf("session end with files: %v", err)
		}
		resp2 := parseResponse(t, out2)
		if resp2["ok"] != true {
			t.Fatalf("expected ok=true, got %v", resp2["ok"])
		}
	})
}

func TestInitWithMachineID(t *testing.T) {
	tmp := t.TempDir()
	mustGit(t, tmp, "init", "-b", "main")
	mustGit(t, tmp, "config", "user.email", "test@example.com")
	mustGit(t, tmp, "config", "user.name", "Test")
	_ = os.WriteFile(filepath.Join(tmp, "README.md"), []byte("init\n"), 0o644)
	mustGit(t, tmp, "add", "README.md")
	mustGit(t, tmp, "commit", "-m", "init")
	mustGit(t, tmp, "update-ref", "refs/remotes/origin/main", "HEAD")

	old, _ := os.Getwd()
	_ = os.Chdir(tmp)
	defer os.Chdir(old)

	app := solo.NewApp()
	out, err := runCapture(app, []string{"init", "--machine-id", "test-machine"})
	if err != nil {
		t.Fatalf("init --machine-id: %v", err)
	}
	resp := parseResponse(t, out)
	if resp["ok"] != true {
		t.Fatalf("expected ok=true, got %v", resp["ok"])
	}
}

func TestHandoffCreateMissingSummary(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		_, err := runCapture(app, []string{"handoff", "create", "T-1"})
		if err == nil {
			t.Fatal("expected error for missing summary")
		}
	})
}

func TestAuditShowInvalidID(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		_, err := runCapture(app, []string{"audit", "show", "notanumber"})
		if err == nil {
			t.Fatal("expected error for invalid audit ID")
		}
	})
}

func TestWorktreeCleanupForce(t *testing.T) {
	withSetup(t, func(app *solo.App, _ string) {
		out, err := runCapture(app, []string{"worktree", "cleanup", "--force"})
		if err != nil {
			t.Fatalf("worktree cleanup --force: %v", err)
		}
		resp := parseResponse(t, out)
		if resp["ok"] != true {
			t.Fatalf("expected ok=true, got %v", resp["ok"])
		}
	})
}
