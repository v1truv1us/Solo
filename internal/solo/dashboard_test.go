package solo

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectMetricsShape(t *testing.T) {
	app := NewApp()
	withTempRepoCWD(t, func() {
		if _, err := app.Init("", "environment", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}
		if _, err := app.CreateTask(CreateTaskInput{Title: "Task A"}); err != nil {
			t.Fatalf("create task: %v", err)
		}
		if _, err := app.CreateTask(CreateTaskInput{Title: "Task B"}); err != nil {
			t.Fatalf("create task: %v", err)
		}

		metrics, err := app.CollectMetrics()
		if err != nil {
			t.Fatalf("collect metrics: %v", err)
		}
		if metrics.TasksTotal != 2 {
			t.Fatalf("expected 2 tasks, got %d", metrics.TasksTotal)
		}
		for _, status := range dashboardStatuses {
			if _, ok := metrics.TasksByStatus[status]; !ok {
				t.Fatalf("missing task status key: %s", status)
			}
		}
		prom, err := app.PrometheusMetrics()
		if err != nil {
			t.Fatalf("prom metrics: %v", err)
		}
		required := []string{
			"solo_tasks_total",
			"solo_tasks_by_status",
			"solo_active_sessions",
			"solo_active_reservations",
			"solo_pending_handoffs",
			"solo_worktrees_active",
			"solo_worktrees_cleanup_pending",
			"solo_db_size_bytes",
			"solo_zombie_sessions",
		}
		for _, key := range required {
			if !strings.Contains(prom, key) {
				t.Fatalf("missing metric key %q in output", key)
			}
		}
	})
}

func TestDashboardHandlers(t *testing.T) {
	app := NewApp()
	withTempRepoCWD(t, func() {
		if _, err := app.Init("", "environment", "", false); err != nil {
			t.Fatalf("init: %v", err)
		}
		if _, err := app.CreateTask(CreateTaskInput{Title: "Task A"}); err != nil {
			t.Fatalf("create task: %v", err)
		}

		h := app.DashboardHandler("")

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("GET / status: %d", rr.Code)
		}
		if !strings.Contains(rr.Header().Get("Content-Type"), "text/html") {
			t.Fatalf("expected html content type, got %q", rr.Header().Get("Content-Type"))
		}
		if !strings.Contains(rr.Body.String(), "Solo Dashboard") {
			t.Fatalf("expected dashboard title")
		}

		req = httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
		rr = httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("GET /api/dashboard status: %d body=%s", rr.Code, rr.Body.String())
		}
		if !strings.Contains(rr.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("expected json content type, got %q", rr.Header().Get("Content-Type"))
		}
		if !strings.Contains(rr.Body.String(), "\"metrics\"") {
			t.Fatalf("expected metrics in dashboard payload")
		}

		req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
		rr = httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("GET /metrics status: %d", rr.Code)
		}
		if !strings.Contains(rr.Header().Get("Content-Type"), "text/plain") {
			t.Fatalf("expected text content type, got %q", rr.Header().Get("Content-Type"))
		}
		body, _ := io.ReadAll(rr.Body)
		if !strings.Contains(string(body), "solo_tasks_total") {
			t.Fatalf("expected solo_tasks_total in metrics")
		}
	})
}

func withTempRepoCWD(t *testing.T, fn func()) {
	t.Helper()
	tmp := t.TempDir()
	if err := os.Mkdir(filepath.Join(tmp, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
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
	fn()
}
