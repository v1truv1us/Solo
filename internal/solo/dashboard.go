package solo

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"sort"
	"strings"
)

var dashboardStatuses = []string{"draft", "ready", "active", "completed", "failed", "blocked", "cancelled"}

type MetricsSnapshot struct {
	TasksTotal              int            `json:"tasks_total"`
	TasksByStatus           map[string]int `json:"tasks_by_status"`
	ActiveSessions          int            `json:"active_sessions"`
	ActiveReservations      int            `json:"active_reservations"`
	PendingHandoffs         int            `json:"pending_handoffs"`
	WorktreesActive         int            `json:"worktrees_active"`
	WorktreesCleanupPending int            `json:"worktrees_cleanup_pending"`
	DBSizeBytes             int64          `json:"db_size_bytes"`
	ZombieSessions          int            `json:"zombie_sessions"`
}

func (a *App) CollectMetrics() (MetricsSnapshot, error) {
	return a.withDBMetrics(func(db *sql.DB) (MetricsSnapshot, error) {
		root, err := discoverRepoRoot(".")
		if err != nil {
			return MetricsSnapshot{}, err
		}
		dbPath := root + "/.solo/solo.db"
		var size int64
		if st, err := os.Stat(dbPath); err == nil {
			size = st.Size()
		}

		statusCounts := map[string]int{}
		total := 0
		rows, err := db.Query(`SELECT status, COUNT(*) FROM tasks GROUP BY status`)
		if err != nil {
			return MetricsSnapshot{}, err
		}
		defer rows.Close()
		for rows.Next() {
			var status string
			var count int
			if err := rows.Scan(&status, &count); err != nil {
				return MetricsSnapshot{}, err
			}
			status = canonicalTaskStatus(status)
			statusCounts[status] += count
			total += count
		}
		for _, status := range dashboardStatuses {
			if _, ok := statusCounts[status]; !ok {
				statusCounts[status] = 0
			}
		}

		activeSessions := scalarInt(db, `SELECT COUNT(*) FROM sessions WHERE ended_at IS NULL`)
		activeReservations := scalarInt(db, `SELECT COUNT(*) FROM reservations WHERE active=1`)
		pendingHandoffs := scalarInt(db, `SELECT COUNT(*) FROM handoffs WHERE status='pending'`)
		activeWorktrees := scalarInt(db, `SELECT COUNT(*) FROM worktrees WHERE status='active'`)
		cleanupPending := scalarInt(db, `SELECT COUNT(*) FROM worktrees WHERE status='cleanup_pending'`)

		zombieCount := 0
		zRows, zErr := db.Query(`SELECT agent_pid FROM sessions WHERE ended_at IS NULL AND agent_pid IS NOT NULL`)
		if zErr == nil {
			defer zRows.Close()
			for zRows.Next() {
				var pid int
				_ = zRows.Scan(&pid)
				if isProcessDead(pid) {
					zombieCount++
				}
			}
		}

		return MetricsSnapshot{
			TasksTotal:              total,
			TasksByStatus:           statusCounts,
			ActiveSessions:          activeSessions,
			ActiveReservations:      activeReservations,
			PendingHandoffs:         pendingHandoffs,
			WorktreesActive:         activeWorktrees,
			WorktreesCleanupPending: cleanupPending,
			DBSizeBytes:             size,
			ZombieSessions:          zombieCount,
		}, nil
	})
}

func (a *App) DashboardSnapshot(taskLimit, sessionLimit int) (map[string]any, error) {
	if taskLimit <= 0 {
		taskLimit = 10
	}
	if sessionLimit <= 0 {
		sessionLimit = 10
	}

	metrics, err := a.CollectMetrics()
	if err != nil {
		return nil, err
	}
	health, err := a.Health()
	if err != nil {
		return nil, err
	}
	recentTasksResp, err := a.ListTasks("", "", false, taskLimit, 0)
	if err != nil {
		return nil, err
	}
	recentTasks, _ := recentTasksResp["tasks"].([]map[string]any)
	if recentTasks == nil {
		recentTasks = []map[string]any{}
	}
	recentSessionsResp, err := a.ListSessions("", "", false)
	if err != nil {
		return nil, err
	}
	allSessions, _ := recentSessionsResp["sessions"].([]map[string]any)
	if allSessions == nil {
		allSessions = []map[string]any{}
	}
	if len(allSessions) > sessionLimit {
		allSessions = allSessions[:sessionLimit]
	}

	worktreesResp, err := a.ListWorktrees()
	if err != nil {
		return nil, err
	}
	worktrees, _ := worktreesResp["worktrees"].([]map[string]any)
	if worktrees == nil {
		worktrees = []map[string]any{}
	}
	pendingHandoffsResp, err := a.ListHandoffs("", "pending")
	if err != nil {
		return nil, err
	}
	pendingHandoffs, _ := pendingHandoffsResp["handoffs"].([]map[string]any)
	if pendingHandoffs == nil {
		pendingHandoffs = []map[string]any{}
	}

	return map[string]any{
		"generated_at":    nowISO(),
		"health":          health,
		"metrics":         metrics,
		"recent_tasks":    recentTasks,
		"recent_sessions": allSessions,
		"worktrees": map[string]any{
			"total": worktreesResp["total"],
			"max":   worktreesResp["max"],
			"items": worktrees,
		},
		"pending_handoffs": pendingHandoffs,
	}, nil
}

func (a *App) PrometheusMetrics() (string, error) {
	m, err := a.CollectMetrics()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("# HELP solo_tasks_total Total number of tasks in Solo.\n")
	b.WriteString("# TYPE solo_tasks_total gauge\n")
	b.WriteString(fmt.Sprintf("solo_tasks_total %d\n", m.TasksTotal))

	b.WriteString("# HELP solo_tasks_by_status Number of tasks by canonical status.\n")
	b.WriteString("# TYPE solo_tasks_by_status gauge\n")
	statusKeys := make([]string, 0, len(m.TasksByStatus))
	for k := range m.TasksByStatus {
		statusKeys = append(statusKeys, k)
	}
	sort.Strings(statusKeys)
	for _, status := range statusKeys {
		b.WriteString(fmt.Sprintf("solo_tasks_by_status{status=\"%s\"} %d\n", status, m.TasksByStatus[status]))
	}

	b.WriteString("# HELP solo_active_sessions Number of active sessions.\n")
	b.WriteString("# TYPE solo_active_sessions gauge\n")
	b.WriteString(fmt.Sprintf("solo_active_sessions %d\n", m.ActiveSessions))

	b.WriteString("# HELP solo_active_reservations Number of active task reservations.\n")
	b.WriteString("# TYPE solo_active_reservations gauge\n")
	b.WriteString(fmt.Sprintf("solo_active_reservations %d\n", m.ActiveReservations))

	b.WriteString("# HELP solo_pending_handoffs Number of handoffs waiting to be picked up.\n")
	b.WriteString("# TYPE solo_pending_handoffs gauge\n")
	b.WriteString(fmt.Sprintf("solo_pending_handoffs %d\n", m.PendingHandoffs))

	b.WriteString("# HELP solo_worktrees_active Number of active worktrees.\n")
	b.WriteString("# TYPE solo_worktrees_active gauge\n")
	b.WriteString(fmt.Sprintf("solo_worktrees_active %d\n", m.WorktreesActive))

	b.WriteString("# HELP solo_worktrees_cleanup_pending Number of worktrees pending cleanup.\n")
	b.WriteString("# TYPE solo_worktrees_cleanup_pending gauge\n")
	b.WriteString(fmt.Sprintf("solo_worktrees_cleanup_pending %d\n", m.WorktreesCleanupPending))

	b.WriteString("# HELP solo_db_size_bytes SQLite database size in bytes.\n")
	b.WriteString("# TYPE solo_db_size_bytes gauge\n")
	b.WriteString(fmt.Sprintf("solo_db_size_bytes %d\n", m.DBSizeBytes))

	b.WriteString("# HELP solo_zombie_sessions Number of sessions with dead owning process IDs.\n")
	b.WriteString("# TYPE solo_zombie_sessions gauge\n")
	b.WriteString(fmt.Sprintf("solo_zombie_sessions %d\n", m.ZombieSessions))

	return b.String(), nil
}

func (a *App) DashboardHandler() http.Handler {
	mux := http.NewServeMux()
	page := template.Must(template.New("dashboard").Parse(dashboardHTML))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = page.Execute(w, map[string]any{"title": "Solo Dashboard"})
	})

	mux.HandleFunc("/api/dashboard", func(w http.ResponseWriter, r *http.Request) {
		snapshot, err := a.DashboardSnapshot(12, 12)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = writeJSONTo(w, map[string]any{"ok": true, "data": snapshot})
	})

	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		payload, err := a.PrometheusMetrics()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write([]byte(payload))
	})

	return mux
}

func (a *App) RunDashboard(addr string) error {
	if strings.TrimSpace(addr) == "" {
		addr = ":8081"
	}
	return http.ListenAndServe(addr, a.DashboardHandler())
}

func (a *App) withDBMetrics(op func(*sql.DB) (MetricsSnapshot, error)) (MetricsSnapshot, error) {
	root, err := discoverRepoRoot(".")
	if err != nil {
		return MetricsSnapshot{}, err
	}
	dbPath := root + "/.solo/solo.db"
	db, err := openDB(dbPath)
	if err != nil {
		return MetricsSnapshot{}, err
	}
	defer db.Close()
	if err := applySchema(db); err != nil {
		return MetricsSnapshot{}, err
	}
	lazyZombieScan(db)
	return op(db)
}

func scalarInt(db *sql.DB, query string) int {
	var value int
	_ = db.QueryRow(query).Scan(&value)
	return value
}

func writeJSONTo(w http.ResponseWriter, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

const dashboardHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<title>Solo Dashboard</title>
<style>
body { font-family: ui-sans-serif, system-ui, -apple-system, Segoe UI, Roboto, sans-serif; margin: 16px; background: #0f172a; color: #e2e8f0; }
.card { background: #111827; border: 1px solid #334155; border-radius: 8px; padding: 12px; margin-bottom: 12px; }
h1,h2 { margin: 0 0 8px 0; }
.grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); gap: 8px; }
.metric { background: #1e293b; border-radius: 6px; padding: 8px; }
pre { overflow-x: auto; background: #0b1220; padding: 10px; border-radius: 6px; }
a { color: #93c5fd; }
</style>
</head>
<body>
  <h1>Solo Dashboard</h1>
  <p>Read-only live status. JSON: <a href="/api/dashboard">/api/dashboard</a> • Metrics: <a href="/metrics">/metrics</a></p>

  <div class="card">
    <h2>Core Metrics</h2>
    <div id="metrics" class="grid"></div>
  </div>

  <div class="card">
    <h2>Task Counts by Status</h2>
    <pre id="task-status">loading…</pre>
  </div>

  <div class="card">
    <h2>Recent Tasks</h2>
    <pre id="recent-tasks">loading…</pre>
  </div>

  <div class="card">
    <h2>Recent Sessions</h2>
    <pre id="recent-sessions">loading…</pre>
  </div>

  <div class="card">
    <h2>Pending Handoffs</h2>
    <pre id="handoffs">loading…</pre>
  </div>

<script>
async function refresh() {
  const res = await fetch('/api/dashboard');
  const payload = await res.json();
  const data = payload.data || {};
  const m = data.metrics || {};
  const metricsEl = document.getElementById('metrics');
  const cards = [
    ['Tasks total', m.tasks_total],
    ['Active sessions', m.active_sessions],
    ['Active reservations', m.active_reservations],
    ['Pending handoffs', m.pending_handoffs],
    ['Worktrees active', m.worktrees_active],
    ['Cleanup pending', m.worktrees_cleanup_pending],
    ['DB size bytes', m.db_size_bytes],
    ['Zombie sessions', m.zombie_sessions],
  ];
  metricsEl.innerHTML = cards.map(function(pair) {
    var k = pair[0];
    var v = pair[1];
    return '<div class="metric"><strong>' + k + '</strong><div>' + (v == null ? 0 : v) + '</div></div>';
  }).join('');

  document.getElementById('task-status').textContent = JSON.stringify(m.tasks_by_status || {}, null, 2);
  document.getElementById('recent-tasks').textContent = JSON.stringify(data.recent_tasks || [], null, 2);
  document.getElementById('recent-sessions').textContent = JSON.stringify(data.recent_sessions || [], null, 2);
  document.getElementById('handoffs').textContent = JSON.stringify(data.pending_handoffs || [], null, 2);
}
refresh();
setInterval(refresh, 5000);
</script>
</body>
</html>`
