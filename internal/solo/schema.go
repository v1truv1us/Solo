package solo

import (
	"context"
	"database/sql"
	"fmt"
)

func applySchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS tasks (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL CHECK (length(trim(title)) > 0),
			description TEXT DEFAULT '',
			type TEXT NOT NULL DEFAULT 'task' CHECK (type IN ('task', 'bug', 'feature', 'chore', 'spike')),
			status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'ready', 'active', 'completed', 'failed', 'blocked', 'cancelled')),
			priority INTEGER NOT NULL DEFAULT 3 CHECK (priority BETWEEN 2 AND 5),
			acceptance_criteria TEXT DEFAULT '',
			definition_of_done TEXT DEFAULT '',
			affected_files TEXT DEFAULT '[]',
			labels TEXT DEFAULT '[]',
			parent_task TEXT REFERENCES tasks(id) ON DELETE SET NULL,
			version INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
			updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_priority ON tasks(priority)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_parent ON tasks(parent_task)`,
		`CREATE TRIGGER IF NOT EXISTS tasks_updated_at AFTER UPDATE ON tasks FOR EACH ROW BEGIN
			UPDATE tasks SET updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = NEW.id;
		END`,
		`CREATE TABLE IF NOT EXISTS task_dependencies (
			task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
			depends_on TEXT NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
			PRIMARY KEY (task_id, depends_on),
			CHECK (task_id != depends_on)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_deps_task ON task_dependencies(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_deps_depends_on ON task_dependencies(depends_on)`,
		`CREATE TABLE IF NOT EXISTS reservations (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
			worker_id TEXT NOT NULL CHECK (length(trim(worker_id)) > 0),
			active INTEGER NOT NULL DEFAULT 1,
			reserved_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
			expires_at TEXT NOT NULL,
			ttl_sec INTEGER NOT NULL DEFAULT 3600,
			released_at TEXT,
			release_reason TEXT CHECK (release_reason IN ('completed', 'expired', 'handoff', 'manual', 'recovered', NULL)),
			worktree_path TEXT,
			machine_id TEXT NOT NULL DEFAULT 'default',
			token TEXT
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_reservations_active_task ON reservations(task_id) WHERE active = 1`,
		`CREATE INDEX IF NOT EXISTS idx_reservations_expires ON reservations(expires_at) WHERE active = 1`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
			reservation_id TEXT NOT NULL REFERENCES reservations(id) ON DELETE RESTRICT,
			worker_id TEXT NOT NULL,
			agent_pid INTEGER,
			worktree_path TEXT,
			branch TEXT,
			started_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
			ended_at TEXT,
			result TEXT CHECK (result IN ('completed', 'failed', 'handoff', 'interrupted', 'abandoned', NULL)),
			result_detail TEXT,
			exit_code INTEGER,
			notes TEXT DEFAULT '',
			commits TEXT DEFAULT '[]',
			files_changed TEXT DEFAULT '[]',
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_task ON sessions(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_reservation ON sessions(reservation_id)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_active ON sessions(task_id) WHERE ended_at IS NULL`,
		`CREATE TABLE IF NOT EXISTS handoffs (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
			session_id TEXT REFERENCES sessions(id),
			reservation_id TEXT REFERENCES reservations(id),
			from_worker TEXT NOT NULL,
			to_worker TEXT,
			summary TEXT NOT NULL CHECK (length(trim(summary)) > 0),
			remaining_work TEXT DEFAULT '',
			files_touched TEXT DEFAULT '[]',
			commits_json TEXT DEFAULT '[]',
			error_context TEXT,
			worktree_status TEXT CHECK (worktree_status IN ('clean', 'dirty_staged', 'dirty_unstaged', 'conflicts', NULL)),
			status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'expired')),
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
			accepted_at TEXT,
			expires_at TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_handoffs_task ON handoffs(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_handoffs_status ON handoffs(status) WHERE status='pending'`,
		`CREATE TABLE IF NOT EXISTS worktrees (
			path TEXT PRIMARY KEY,
			task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
			branch_name TEXT NOT NULL,
			base_ref TEXT NOT NULL DEFAULT 'origin/main',
			base_commit_sha TEXT,
			status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'cleanup_pending')),
			disk_usage_bytes INTEGER,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_worktrees_task ON worktrees(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_worktrees_status ON worktrees(status)`,
		`CREATE TABLE IF NOT EXISTS recovery_records (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT NOT NULL REFERENCES tasks(id),
			session_id TEXT REFERENCES sessions(id),
			reason TEXT NOT NULL CHECK (reason IN ('crash_detected', 'reservation_expired', 'manual_recovery', 'session_timeout', 'worktree_conflict')),
			previous_status TEXT NOT NULL,
			recovered_to TEXT NOT NULL DEFAULT 'ready',
			worktree_state TEXT CHECK (worktree_state IN ('clean', 'dirty', 'conflicts', 'missing', NULL)),
			uncommitted_files TEXT DEFAULT '[]',
			diagnostic_json TEXT,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		)`,
		`CREATE TABLE IF NOT EXISTS audit_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT REFERENCES tasks(id) ON DELETE CASCADE,
			event_type TEXT NOT NULL,
			actor_type TEXT NOT NULL CHECK (actor_type IN ('human', 'agent', 'system')),
			actor_id TEXT NOT NULL,
			old_value_json TEXT,
			new_value_json TEXT,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_task ON audit_events(task_id, created_at)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS tasks_fts USING fts5(id, title, description, acceptance_criteria, content='tasks', content_rowid='rowid')`,
		`CREATE TRIGGER IF NOT EXISTS tasks_fts_ai AFTER INSERT ON tasks BEGIN
			INSERT INTO tasks_fts(rowid, id, title, description, acceptance_criteria)
			VALUES (new.rowid, new.id, new.title, new.description, new.acceptance_criteria);
		END`,
		`CREATE TRIGGER IF NOT EXISTS tasks_fts_au AFTER UPDATE ON tasks BEGIN
			INSERT INTO tasks_fts(tasks_fts, rowid, id, title, description, acceptance_criteria)
			VALUES ('delete', old.rowid, old.id, old.title, old.description, old.acceptance_criteria);
			INSERT INTO tasks_fts(rowid, id, title, description, acceptance_criteria)
			VALUES (new.rowid, new.id, new.title, new.description, new.acceptance_criteria);
		END`,
		`CREATE TRIGGER IF NOT EXISTS tasks_fts_ad AFTER DELETE ON tasks BEGIN
			INSERT INTO tasks_fts(tasks_fts, rowid, id, title, description, acceptance_criteria)
			VALUES ('delete', old.rowid, old.id, old.title, old.description, old.acceptance_criteria);
		END`,
		`CREATE TABLE IF NOT EXISTS schema_version (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
			description TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS config (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
	}
	ctx := context.Background()
	return withImmediateTx(ctx, db, func(conn *sql.Conn) error {
		for _, stmt := range stmts {
			if _, err := conn.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("schema statement failed: %w", err)
			}
		}
		_, _ = conn.ExecContext(ctx, `INSERT OR IGNORE INTO schema_version (version, description) VALUES (1, 'Solo v13 initial schema')`)
		_, _ = conn.ExecContext(ctx, `UPDATE tasks SET status='draft' WHERE status IN ('open','triaged')`)
		_, _ = conn.ExecContext(ctx, `UPDATE tasks SET status='active' WHERE status IN ('in_progress','in_review')`)
		_, _ = conn.ExecContext(ctx, `UPDATE tasks SET status='completed' WHERE status='done'`)
		_, _ = conn.ExecContext(ctx, `UPDATE tasks SET priority=2 WHERE priority < 2`)
		_, _ = conn.ExecContext(ctx, `INSERT OR IGNORE INTO schema_version (version, description) VALUES (2, 'Canonical task lifecycle + priority semantics')`)
		_, _ = conn.ExecContext(ctx, `DELETE FROM worktrees WHERE status='cleaned'`)
		_, _ = conn.ExecContext(ctx, `INSERT OR IGNORE INTO schema_version (version, description) VALUES (3, 'Remove stale cleaned worktree rows')`)
		return nil
	})
}

func setDefaultConfig(db *sql.DB, machineID string) error {
	defaults := map[string]string{
		"machine_id":      machineID,
		"default_ttl_sec": "3600",
		"max_ttl_sec":     "86400",
		"max_worktrees":   "5",
		"base_ref":        "origin/main",
		"worktree_dir":    ".solo/worktrees",
		"session_ttl_sec": "14400",
		"max_tokens":      "8000",
	}
	ctx := context.Background()
	return withImmediateTx(ctx, db, func(conn *sql.Conn) error {
		for k, v := range defaults {
			if _, err := conn.ExecContext(ctx, `INSERT OR IGNORE INTO config (key, value) VALUES (?, ?)`, k, v); err != nil {
				return err
			}
		}
		return nil
	})
}
