package solo

import (
	"context"
	"database/sql"
	"strings"
)

var validTransitions = map[string][]string{
	"open":        {"triaged", "ready", "cancelled"},
	"triaged":     {"ready", "blocked", "cancelled"},
	"ready":       {"in_progress", "blocked", "cancelled"},
	"in_progress": {"in_review", "blocked", "ready", "cancelled"},
	"in_review":   {"done", "in_progress", "blocked", "cancelled"},
	"blocked":     {"ready", "triaged", "cancelled"},
	"done":        {},
	"cancelled":   {"open"},
}

type CreateTaskInput struct {
	Title              string
	Description        string
	Type               string
	Priority           int
	AcceptanceCriteria string
	DefinitionOfDone   string
	AffectedFiles      []string
	Labels             []string
	ParentTask         string
	Dependencies       []string
}

func (a *App) CreateTask(in CreateTaskInput) (map[string]any, error) {
	if strings.TrimSpace(in.Title) == "" {
		return nil, ErrInvalidArgument("title is required")
	}
	if in.Type == "" {
		in.Type = "task"
	}
	if in.Priority < 1 || in.Priority > 5 {
		in.Priority = 3
	}
	return a.withDB(func(db *sql.DB) (map[string]any, error) {
		ctx := context.Background()
		var taskID string
		if err := withImmediateTx(ctx, db, func(conn *sql.Conn) error {
			row := conn.QueryRowContext(ctx, `SELECT 'T-' || (COALESCE((SELECT MAX(CAST(SUBSTR(id, 3) AS INTEGER)) FROM tasks), 0) + 1)`)
			if err := row.Scan(&taskID); err != nil {
				return err
			}
			affected := mustJSON(in.AffectedFiles)
			labels := mustJSON(in.Labels)
			var parent any
			if strings.TrimSpace(in.ParentTask) != "" {
				parent = in.ParentTask
			}
			if _, err := conn.ExecContext(ctx, `INSERT INTO tasks (id, title, description, type, priority, acceptance_criteria, definition_of_done, affected_files, labels, parent_task)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				taskID, in.Title, in.Description, in.Type, in.Priority, in.AcceptanceCriteria, in.DefinitionOfDone, affected, labels, parent); err != nil {
				return err
			}
			for _, dep := range in.Dependencies {
				if err := ensureNoCycle(ctx, conn, taskID, dep); err != nil {
					return err
				}
				if _, err := conn.ExecContext(ctx, `INSERT INTO task_dependencies (task_id, depends_on) VALUES (?, ?)`, taskID, dep); err != nil {
					return err
				}
			}
			return writeAudit(ctx, conn, taskID, "task.created", "human", "cli", nil, map[string]any{"id": taskID, "status": "open"})
		}); err != nil {
			return nil, err
		}
		row := db.QueryRow(`SELECT id,title,type,status,priority,version,created_at FROM tasks WHERE id=?`, taskID)
		var id, title, typ, status, created string
		var priority, version int
		if err := row.Scan(&id, &title, &typ, &status, &priority, &version, &created); err != nil {
			return nil, err
		}
		return map[string]any{"task": map[string]any{
			"id": id, "title": title, "type": typ, "status": status,
			"priority": priority, "version": version, "created_at": created,
		}}, nil
	})
}

func ensureNoCycle(ctx context.Context, conn *sql.Conn, taskID, dep string) error {
	row := conn.QueryRowContext(ctx, `WITH RECURSIVE ancestors(id) AS (
		SELECT depends_on FROM task_dependencies WHERE task_id = ?
		UNION ALL
		SELECT td.depends_on FROM task_dependencies td JOIN ancestors a ON td.task_id = a.id
	)
	SELECT 1 FROM ancestors WHERE id = ? LIMIT 1`, dep, taskID)
	var one int
	err := row.Scan(&one)
	if err == nil {
		return errCircularDependency()
	}
	if err == sql.ErrNoRows {
		return nil
	}
	return err
}

func (a *App) ListTasks(status, label string, available bool, limit, offset int) (map[string]any, error) {
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return a.withDB(func(db *sql.DB) (map[string]any, error) {
		query := `SELECT t.id,t.title,t.type,t.status,t.priority,t.labels,t.version,t.updated_at
		FROM tasks t WHERE 1=1`
		args := []any{}
		if status != "" {
			query += ` AND t.status=?`
			args = append(args, status)
		}
		if available {
			query += ` AND t.status='ready' AND NOT EXISTS (SELECT 1 FROM reservations r WHERE r.task_id=t.id AND r.active=1)`
		}
		if label != "" {
			query += ` AND EXISTS (SELECT 1 FROM json_each(t.labels) WHERE value=?)`
			args = append(args, label)
		}
		query += ` ORDER BY CAST(SUBSTR(t.id,3) AS INTEGER) DESC LIMIT ? OFFSET ?`
		args = append(args, limit, offset)
		rows, err := db.Query(query, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		tasks := make([]map[string]any, 0)
		for rows.Next() {
			var id, title, typ, st, labelsJSON, updated string
			var p, v int
			if err := rows.Scan(&id, &title, &typ, &st, &p, &labelsJSON, &v, &updated); err != nil {
				return nil, err
			}
			labels, err := parseJSONList[string](labelsJSON)
			if err != nil {
				return nil, err
			}
			tasks = append(tasks, map[string]any{
				"id": id, "title": title, "type": typ, "status": st,
				"priority": p, "labels": labels, "version": v, "updated_at": updated,
			})
		}
		countQ := `SELECT COUNT(*) FROM tasks t WHERE 1=1`
		countArgs := []any{}
		if status != "" {
			countQ += ` AND t.status=?`
			countArgs = append(countArgs, status)
		}
		if available {
			countQ += ` AND t.status='ready' AND NOT EXISTS (SELECT 1 FROM reservations r WHERE r.task_id=t.id AND r.active=1)`
		}
		if label != "" {
			countQ += ` AND EXISTS (SELECT 1 FROM json_each(t.labels) WHERE value=?)`
			countArgs = append(countArgs, label)
		}
		var total int
		if err := db.QueryRow(countQ, countArgs...).Scan(&total); err != nil {
			return nil, err
		}
		return map[string]any{"tasks": tasks, "total": total, "limit": limit, "offset": offset}, nil
	})
}

func (a *App) ShowTask(taskID string) (map[string]any, error) {
	return a.withDB(func(db *sql.DB) (map[string]any, error) {
		task, err := readTaskBasic(db, taskID)
		if err != nil {
			return nil, err
		}
		deps := []map[string]any{}
		rows, err := db.Query(`SELECT d.depends_on, t.title, t.status FROM task_dependencies d JOIN tasks t ON t.id=d.depends_on WHERE d.task_id=?`, taskID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, title, status string
			if err := rows.Scan(&id, &title, &status); err != nil {
				rows.Close()
				return nil, err
			}
			deps = append(deps, map[string]any{"task_id": id, "title": title, "status": status})
		}
		rows.Close()
		var rid, worker, expires string
		var activeRes any
		if err := db.QueryRow(`SELECT id, worker_id, expires_at FROM reservations WHERE task_id=? AND active=1`, taskID).Scan(&rid, &worker, &expires); err == nil {
			activeRes = map[string]any{"id": rid, "worker_id": worker, "expires_at": expires}
		}
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE task_id=?`, taskID).Scan(&count); err != nil {
			return nil, err
		}
		return map[string]any{"task": task, "dependencies": deps, "active_reservation": activeRes, "session_count": count}, nil
	})
}

func (a *App) UpdateTaskStatus(taskID, newStatus string, expectedVersion int) (map[string]any, error) {
	if newStatus == "" {
		return nil, ErrInvalidArgument("--status is required")
	}
	if expectedVersion <= 0 {
		return nil, ErrInvalidArgument("--version is required")
	}
	return a.withDB(func(db *sql.DB) (map[string]any, error) {
		ctx := context.Background()
		var updated bool
		if err := withImmediateTx(ctx, db, func(conn *sql.Conn) error {
			if locked, err := hasActiveReservation(ctx, conn, taskID); err != nil {
				return err
			} else if locked {
				return errTaskLocked(taskID)
			}
			var current string
			var version int
			if err := conn.QueryRowContext(ctx, `SELECT status, version FROM tasks WHERE id=?`, taskID).Scan(&current, &version); err != nil {
				if err == sql.ErrNoRows {
					return errTaskNotFound(taskID)
				}
				return err
			}
			if version != expectedVersion {
				return errVersionConflict()
			}
			if !transitionAllowed(current, newStatus) {
				return errInvalidTransition(current, newStatus, validTransitions[current])
			}
			res, err := conn.ExecContext(ctx, `UPDATE tasks SET status=?, version=version+1, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id=? AND version=?`, newStatus, taskID, expectedVersion)
			if err != nil {
				return err
			}
			n, _ := res.RowsAffected()
			if n == 0 {
				return errVersionConflict()
			}
			updated = true
			return writeAudit(ctx, conn, taskID, "task.update", "human", "cli", map[string]any{"status": current}, map[string]any{"status": newStatus})
		}); err != nil {
			return nil, err
		}
		if !updated {
			return nil, errVersionConflict()
		}
		task, err := readTaskBasic(db, taskID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"task": task}, nil
	})
}

func transitionAllowed(from, to string) bool {
	for _, v := range validTransitions[from] {
		if v == to {
			return true
		}
	}
	return false
}

func hasActiveReservation(ctx context.Context, conn *sql.Conn, taskID string) (bool, error) {
	var one int
	err := conn.QueryRowContext(ctx, `SELECT 1 FROM reservations WHERE task_id=? AND active=1 LIMIT 1`, taskID).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (a *App) ForceReady(taskID string, expectedVersion int) (map[string]any, error) {
	return a.UpdateTaskStatus(taskID, "ready", expectedVersion)
}

func (a *App) TaskDeps(taskID string) (map[string]any, error) {
	return a.withDB(func(db *sql.DB) (map[string]any, error) {
		rows, err := db.Query(`WITH RECURSIVE deps(id, depth) AS (
			SELECT depends_on, 1 FROM task_dependencies WHERE task_id=?
			UNION ALL
			SELECT td.depends_on, deps.depth+1 FROM task_dependencies td JOIN deps ON td.task_id=deps.id
		)
		SELECT d.id, t.title, t.status, MIN(d.depth) FROM deps d JOIN tasks t ON t.id=d.id GROUP BY d.id,t.title,t.status ORDER BY MIN(d.depth), d.id`, taskID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		deps := []map[string]any{}
		for rows.Next() {
			var id, title, status string
			var depth int
			if err := rows.Scan(&id, &title, &status, &depth); err != nil {
				return nil, err
			}
			deps = append(deps, map[string]any{"task_id": id, "title": title, "status": status, "depth": depth})
		}
		return map[string]any{"task_id": taskID, "dependencies": deps}, nil
	})
}

func (a *App) Search(query, status string, limit int) (map[string]any, error) {
	if limit <= 0 {
		limit = 10
	}
	return a.withDB(func(db *sql.DB) (map[string]any, error) {
		sqlQ := `SELECT t.id, t.title, t.status, bm25(tasks_fts) as rank,
			snippet(tasks_fts, 2, '', '', '...', 20)
		FROM tasks_fts JOIN tasks t ON tasks_fts.id=t.id
		WHERE tasks_fts MATCH ?`
		args := []any{query}
		if status != "" {
			sqlQ += ` AND t.status=?`
			args = append(args, status)
		}
		sqlQ += ` ORDER BY rank LIMIT ?`
		args = append(args, limit)
		rows, err := db.Query(sqlQ, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		results := []map[string]any{}
		for rows.Next() {
			var id, title, st, snippet string
			var rank float64
			if err := rows.Scan(&id, &title, &st, &rank, &snippet); err != nil {
				return nil, err
			}
			results = append(results, map[string]any{"task_id": id, "title": title, "status": st, "fts5_rank": rank, "snippet": snippet})
		}
		return map[string]any{"results": results, "total": len(results)}, nil
	})
}

func decodeCommitShas(shas []string) []map[string]string {
	out := make([]map[string]string, 0, len(shas))
	for _, sha := range shas {
		if strings.TrimSpace(sha) == "" {
			continue
		}
		out = append(out, map[string]string{"sha": sha, "message": ""})
	}
	return out
}

func (a *App) TaskTree(taskID string) (map[string]any, error) {
	return a.withDB(func(db *sql.DB) (map[string]any, error) {
		rows, err := db.Query(`WITH RECURSIVE tree(id, parent_task, depth) AS (
			SELECT id, parent_task, 0 FROM tasks WHERE id=?
			UNION ALL
			SELECT t.id, t.parent_task, tree.depth+1 FROM tasks t JOIN tree ON t.parent_task=tree.id
		)
		SELECT t.id, t.parent_task, t.title, t.status, t.priority, tree.depth
		FROM tree JOIN tasks t ON t.id=tree.id
		ORDER BY tree.depth, CAST(SUBSTR(t.id,3) AS INTEGER)`, taskID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		nodes := []map[string]any{}
		for rows.Next() {
			var id, title, status string
			var parent sql.NullString
			var priority, depth int
			if err := rows.Scan(&id, &parent, &title, &status, &priority, &depth); err != nil {
				return nil, err
			}
			var parentVal any
			if parent.Valid {
				parentVal = parent.String
			}
			nodes = append(nodes, map[string]any{"id": id, "parent_task": parentVal, "title": title, "status": status, "priority": priority, "depth": depth})
		}
		if len(nodes) == 0 {
			return nil, errTaskNotFound(taskID)
		}
		return map[string]any{"task_id": taskID, "nodes": nodes}, nil
	})
}

func (a *App) UpdateTask(taskID, title, description, priority, parent string, labels, affectedFiles []string, expectedVersion int) (map[string]any, error) {
	if expectedVersion <= 0 {
		return nil, ErrInvalidArgument("--version is required")
	}
	return a.withDB(func(db *sql.DB) (map[string]any, error) {
		ctx := context.Background()
		var updated bool
		if err := withImmediateTx(ctx, db, func(conn *sql.Conn) error {
			var currentVersion int
			if err := conn.QueryRowContext(ctx, `SELECT version FROM tasks WHERE id=?`, taskID).Scan(&currentVersion); err != nil {
				if err == sql.ErrNoRows {
					return errTaskNotFound(taskID)
				}
				return err
			}
			if locked, err := hasActiveReservation(ctx, conn, taskID); err != nil {
				return err
			} else if locked {
				return errTaskLocked(taskID)
			}
			if currentVersion != expectedVersion {
				return errVersionConflict()
			}
			updates := []string{"version = version + 1", "updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')"}
			args := []any{}
			if title != "" {
				updates = append(updates, "title = ?")
				args = append(args, title)
			}
			if description != "" {
				updates = append(updates, "description = ?")
				args = append(args, description)
			}
			if priority != "" {
				updates = append(updates, "priority = ?")
				args = append(args, priority)
			}
			if parent != "" {
				updates = append(updates, "parent_task = ?")
				args = append(args, parent)
			}
			if len(labels) > 0 {
				updates = append(updates, "labels = ?")
				args = append(args, mustJSON(labels))
			}
			if len(affectedFiles) > 0 {
				updates = append(updates, "affected_files = ?")
				args = append(args, mustJSON(affectedFiles))
			}
			args = append(args, taskID, expectedVersion)
			query := `UPDATE tasks SET ` + strings.Join(updates, ", ") + ` WHERE id = ? AND version = ?`
			res, err := conn.ExecContext(ctx, query, args...)
			if err != nil {
				return err
			}
			n, _ := res.RowsAffected()
			if n == 0 {
				return errVersionConflict()
			}
			updated = true
			return writeAudit(ctx, conn, taskID, "task.update", "human", "cli", nil, map[string]any{"title": title, "description": description, "priority": priority})
		}); err != nil {
			return nil, err
		}
		if !updated {
			return nil, errVersionConflict()
		}
		task, err := readTaskBasic(db, taskID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"task": task}, nil
	})
}
