package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/v1truv1us/solo/internal/output"
)

// Task represents a task record.
type Task struct {
	ID                 string   `json:"id"`
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	Type               string   `json:"type"`
	Status             string   `json:"status"`
	Priority           int      `json:"priority"`
	AcceptanceCriteria string   `json:"acceptance_criteria"`
	DefinitionOfDone   string   `json:"definition_of_done"`
	AffectedFiles      []string `json:"affected_files"`
	Labels             []string `json:"labels"`
	ParentTask         *string  `json:"parent_task"`
	Version            int      `json:"version"`
	CreatedAt          string   `json:"created_at"`
	UpdatedAt          string   `json:"updated_at"`
}

// CreateTaskParams holds parameters for task creation.
type CreateTaskParams struct {
	Title              string
	Description        string
	Type               string
	Priority           int
	AcceptanceCriteria string
	DefinitionOfDone   string
	AffectedFiles      []string
	Labels             []string
	ParentTask         *string
	Dependencies       []string
}

// scanTask scans a task from a row, decoding JSON arrays properly (invariant #2).
func scanTask(scanner interface{ Scan(...interface{}) error }) (*Task, error) {
	var task Task
	var affJSON, labJSON string
	err := scanner.Scan(&task.ID, &task.Title, &task.Description, &task.Type, &task.Status,
		&task.Priority, &task.AcceptanceCriteria, &task.DefinitionOfDone,
		&affJSON, &labJSON, &task.ParentTask, &task.Version,
		&task.CreatedAt, &task.UpdatedAt)
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(affJSON), &task.AffectedFiles)
	json.Unmarshal([]byte(labJSON), &task.Labels)
	if task.AffectedFiles == nil {
		task.AffectedFiles = []string{}
	}
	if task.Labels == nil {
		task.Labels = []string{}
	}
	return &task, nil
}

const taskColumns = `id, title, description, type, status, priority, acceptance_criteria,
	definition_of_done, affected_files, labels, parent_task, version, created_at, updated_at`

// CreateTask creates a new task inside a BEGIN IMMEDIATE transaction.
func CreateTask(database *sql.DB, p CreateTaskParams) (*Task, error) {
	if strings.TrimSpace(p.Title) == "" {
		return nil, output.NewError(output.ErrInvalidArgument, "title is required", false, "")
	}
	if p.Type == "" {
		p.Type = "task"
	}
	if !IsValidTaskType(p.Type) {
		return nil, output.NewError(output.ErrInvalidArgument,
			fmt.Sprintf("invalid type %q; valid types: %s", p.Type, strings.Join(AllTaskTypes(), ", ")),
			false, "")
	}
	if p.Priority == 0 {
		p.Priority = 3
	}
	if p.Priority < 1 || p.Priority > 5 {
		return nil, output.NewError(output.ErrInvalidArgument, "priority must be between 1 and 5", false, "")
	}

	affectedFilesJSON, _ := json.Marshal(p.AffectedFiles)
	if p.AffectedFiles == nil {
		affectedFilesJSON = []byte("[]")
	}
	labelsJSON, _ := json.Marshal(p.Labels)
	if p.Labels == nil {
		labelsJSON = []byte("[]")
	}

	return WithTxImmediateResult(database, func(conn *sql.Conn) (*Task, error) {
		ctx := context.Background()

		// Validate parent exists if specified
		if p.ParentTask != nil {
			var exists int
			if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM tasks WHERE id = ?", *p.ParentTask).Scan(&exists); err != nil || exists == 0 {
				return nil, output.NewError(output.ErrTaskNotFound,
					fmt.Sprintf("parent task %q not found", *p.ParentTask), false, "")
			}
		}

		// Validate dependencies exist
		for _, depID := range p.Dependencies {
			var exists int
			if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM tasks WHERE id = ?", depID).Scan(&exists); err != nil || exists == 0 {
				return nil, output.NewError(output.ErrTaskNotFound,
					fmt.Sprintf("dependency task %q not found", depID), false, "")
			}
		}

		// Generate ID per spec §6.1
		task, err := scanTask(conn.QueryRowContext(ctx, `
			INSERT INTO tasks (id, title, description, type, priority, acceptance_criteria,
				definition_of_done, affected_files, labels, parent_task)
			VALUES (
				'T-' || (COALESCE(
					(SELECT MAX(CAST(SUBSTR(id, 3) AS INTEGER)) FROM tasks), 0
				) + 1),
				?, ?, ?, ?, ?, ?, ?, ?, ?
			)
			RETURNING `+taskColumns,
			p.Title, p.Description, p.Type, p.Priority, p.AcceptanceCriteria,
			p.DefinitionOfDone, string(affectedFilesJSON), string(labelsJSON), p.ParentTask))
		if err != nil {
			return nil, fmt.Errorf("inserting task: %w", err)
		}

		// Insert dependencies
		for _, depID := range p.Dependencies {
			// Circular dependency check
			var cycleExists int
			err := conn.QueryRowContext(ctx, `
				WITH RECURSIVE dep_chain(tid) AS (
					SELECT ?
					UNION ALL
					SELECT td.depends_on FROM task_dependencies td
					JOIN dep_chain dc ON dc.tid = td.task_id
				)
				SELECT COUNT(*) FROM dep_chain WHERE tid = ?
			`, depID, task.ID).Scan(&cycleExists)
			if err != nil {
				return nil, fmt.Errorf("checking circular dep: %w", err)
			}
			if cycleExists > 0 {
				return nil, output.NewError(output.ErrCircularDependency,
					fmt.Sprintf("adding dependency %s would create a cycle", depID), false, "Review dependency graph")
			}

			if _, err := conn.ExecContext(ctx, "INSERT INTO task_dependencies (task_id, depends_on) VALUES (?, ?)",
				task.ID, depID); err != nil {
				return nil, fmt.Errorf("inserting dependency: %w", err)
			}
		}

		// Audit event
		afterJSON, _ := json.Marshal(task)
		conn.ExecContext(ctx, `INSERT INTO audit_events (task_id, event_type, actor_type, actor_id, new_value_json)
			VALUES (?, 'task.created', 'system', 'cli', ?)`, task.ID, string(afterJSON))

		return task, nil
	})
}

// GetTask retrieves a single task by ID.
func GetTask(database *sql.DB, taskID string) (*Task, error) {
	task, err := scanTask(database.QueryRow(`SELECT `+taskColumns+` FROM tasks WHERE id = ?`, taskID))
	if err == sql.ErrNoRows {
		return nil, output.NewError(output.ErrTaskNotFound,
			fmt.Sprintf("task %q not found", taskID), false, "Check ID with solo task list")
	}
	if err != nil {
		return nil, fmt.Errorf("querying task: %w", err)
	}
	return task, nil
}

// ListTasksParams holds filter parameters for listing tasks.
type ListTasksParams struct {
	Status    string
	Label     string
	Available bool
	Limit     int
	Offset    int
}

// ListTasksResult holds a list of tasks and total count.
type ListTasksResult struct {
	Tasks  []Task `json:"tasks"`
	Total  int    `json:"total"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
}

// ListTasks lists tasks with optional filters.
func ListTasks(database *sql.DB, p ListTasksParams) (*ListTasksResult, error) {
	if p.Limit <= 0 {
		p.Limit = 20
	}

	var where []string
	var args []interface{}

	if p.Available {
		where = append(where, "t.status = 'ready'")
		where = append(where, "NOT EXISTS (SELECT 1 FROM reservations r WHERE r.task_id = t.id AND r.active = 1)")
	} else if p.Status != "" {
		where = append(where, "t.status = ?")
		args = append(args, p.Status)
	}

	if p.Label != "" {
		where = append(where, "EXISTS (SELECT 1 FROM json_each(t.labels) WHERE json_each.value = ?)")
		args = append(args, p.Label)
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := database.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM tasks t %s", whereClause), args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("counting tasks: %w", err)
	}

	query := fmt.Sprintf(`
		SELECT t.id, t.title, t.description, t.type, t.status, t.priority,
			t.acceptance_criteria, t.definition_of_done, t.affected_files, t.labels,
			t.parent_task, t.version, t.created_at, t.updated_at
		FROM tasks t %s
		ORDER BY t.priority ASC, t.created_at ASC
		LIMIT ? OFFSET ?`, whereClause)
	queryArgs := append(args, p.Limit, p.Offset)

	rows, err := database.Query(query, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("listing tasks: %w", err)
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning task: %w", err)
		}
		tasks = append(tasks, *t)
	}
	if tasks == nil {
		tasks = []Task{}
	}

	return &ListTasksResult{Tasks: tasks, Total: total, Limit: p.Limit, Offset: p.Offset}, nil
}

// UpdateTaskParams holds update parameters.
type UpdateTaskParams struct {
	TaskID             string
	Version            int
	Title              *string
	Description        *string
	Type               *string
	Status             *string
	Priority           *int
	AcceptanceCriteria *string
	DefinitionOfDone   *string
	AffectedFiles      *[]string
	Labels             *[]string
	ParentTask         *string
}

// UpdateTask updates a task with OCC check and Strict Lock Rule enforcement.
func UpdateTask(database *sql.DB, p UpdateTaskParams) (*Task, error) {
	return WithTxImmediateResult(database, func(conn *sql.Conn) (*Task, error) {
		ctx := context.Background()

		var currentStatus string
		var currentVersion int
		err := conn.QueryRowContext(ctx, "SELECT status, version FROM tasks WHERE id = ?", p.TaskID).Scan(&currentStatus, &currentVersion)
		if err == sql.ErrNoRows {
			return nil, output.NewError(output.ErrTaskNotFound,
				fmt.Sprintf("task %q not found", p.TaskID), false, "Check ID with solo task list")
		}
		if err != nil {
			return nil, fmt.Errorf("querying task: %w", err)
		}

		// Strict Lock Rule per spec §5.3
		var hasReservation int
		conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM reservations WHERE task_id = ? AND active = 1", p.TaskID).Scan(&hasReservation)
		if hasReservation > 0 {
			return nil, output.NewError(output.ErrTaskLocked,
				fmt.Sprintf("task %s has an active reservation", p.TaskID),
				false, "Wait for agent to finish, or use solo task recover")
		}

		// OCC check
		if p.Version != currentVersion {
			return nil, output.NewError(output.ErrVersionConflict,
				fmt.Sprintf("version mismatch: expected %d, got %d", currentVersion, p.Version),
				true, "Re-read current version, retry")
		}

		// Validate status transition
		if p.Status != nil && *p.Status != currentStatus {
			if !IsValidTransition(currentStatus, *p.Status) {
				valid := ValidTransitions[currentStatus]
				return nil, &output.SoloError{
					Code:             output.ErrInvalidTransition,
					Message:          fmt.Sprintf("Cannot transition from '%s' to '%s'. Valid: %s", currentStatus, *p.Status, strings.Join(valid, ", ")),
					Retryable:        false,
					CurrentStatus:    currentStatus,
					RequestedStatus:  *p.Status,
					ValidTransitions: valid,
				}
			}
		}

		// Build SET clause
		sets := []string{}
		args := []interface{}{}

		if p.Title != nil {
			if strings.TrimSpace(*p.Title) == "" {
				return nil, output.NewError(output.ErrInvalidArgument, "title cannot be empty", false, "")
			}
			sets = append(sets, "title = ?")
			args = append(args, *p.Title)
		}
		if p.Description != nil {
			sets = append(sets, "description = ?")
			args = append(args, *p.Description)
		}
		if p.Type != nil {
			if !IsValidTaskType(*p.Type) {
				return nil, output.NewError(output.ErrInvalidArgument, fmt.Sprintf("invalid type %q", *p.Type), false, "")
			}
			sets = append(sets, "type = ?")
			args = append(args, *p.Type)
		}
		if p.Status != nil {
			sets = append(sets, "status = ?")
			args = append(args, *p.Status)
		}
		if p.Priority != nil {
			if *p.Priority < 1 || *p.Priority > 5 {
				return nil, output.NewError(output.ErrInvalidArgument, "priority must be between 1 and 5", false, "")
			}
			sets = append(sets, "priority = ?")
			args = append(args, *p.Priority)
		}
		if p.AcceptanceCriteria != nil {
			sets = append(sets, "acceptance_criteria = ?")
			args = append(args, *p.AcceptanceCriteria)
		}
		if p.DefinitionOfDone != nil {
			sets = append(sets, "definition_of_done = ?")
			args = append(args, *p.DefinitionOfDone)
		}
		if p.AffectedFiles != nil {
			j, _ := json.Marshal(*p.AffectedFiles)
			sets = append(sets, "affected_files = ?")
			args = append(args, string(j))
		}
		if p.Labels != nil {
			j, _ := json.Marshal(*p.Labels)
			sets = append(sets, "labels = ?")
			args = append(args, string(j))
		}
		if p.ParentTask != nil {
			sets = append(sets, "parent_task = ?")
			args = append(args, *p.ParentTask)
		}

		if len(sets) == 0 {
			return nil, output.NewError(output.ErrInvalidArgument, "no fields to update", false, "")
		}

		sets = append(sets, "version = version + 1")
		query := fmt.Sprintf("UPDATE tasks SET %s WHERE id = ? AND version = ?", strings.Join(sets, ", "))
		args = append(args, p.TaskID, p.Version)

		result, err := conn.ExecContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("updating task: %w", err)
		}
		rowsAffected, _ := result.RowsAffected()
		if rowsAffected == 0 {
			return nil, output.NewError(output.ErrVersionConflict, "version changed during update", true, "Re-read current version, retry")
		}

		// Audit
		task, err := scanTask(conn.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM tasks WHERE id = ?`, p.TaskID))
		if err != nil {
			return nil, err
		}

		afterJSON, _ := json.Marshal(task)
		conn.ExecContext(ctx, `INSERT INTO audit_events (task_id, event_type, actor_type, actor_id, old_value_json, new_value_json)
			VALUES (?, 'task.updated', 'system', 'cli', ?, ?)`,
			p.TaskID, fmt.Sprintf(`{"status":"%s","version":%d}`, currentStatus, currentVersion), string(afterJSON))

		return task, nil
	})
}

// GetTaskDependencies returns the dependencies of a task.
func GetTaskDependencies(database *sql.DB, taskID string) ([]Task, error) {
	rows, err := database.Query(`
		SELECT t.id, t.title, t.description, t.type, t.status, t.priority,
			t.acceptance_criteria, t.definition_of_done, t.affected_files, t.labels,
			t.parent_task, t.version, t.created_at, t.updated_at
		FROM task_dependencies td
		JOIN tasks t ON t.id = td.depends_on
		WHERE td.task_id = ?
		ORDER BY t.id`, taskID)
	if err != nil {
		return nil, fmt.Errorf("querying dependencies: %w", err)
	}
	defer rows.Close()

	var deps []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning dep: %w", err)
		}
		deps = append(deps, *t)
	}
	if deps == nil {
		deps = []Task{}
	}
	return deps, nil
}

// SearchTasks performs FTS5 search across tasks.
func SearchTasks(database *sql.DB, query string, status string, limit int) ([]map[string]interface{}, int, error) {
	if limit <= 0 {
		limit = 10
	}

	var where string
	var args []interface{}
	args = append(args, query)

	if status != "" {
		where = " AND t.status = ?"
		args = append(args, status)
	}

	args = append(args, limit)

	rows, err := database.Query(fmt.Sprintf(`
		SELECT t.id, t.title, t.status, bm25(tasks_fts) as rank,
			snippet(tasks_fts, 2, '...', '...', '...', 32) as snippet
		FROM tasks_fts
		JOIN tasks t ON tasks_fts.id = t.id
		WHERE tasks_fts MATCH ?%s
		ORDER BY rank
		LIMIT ?`, where), args...)
	if err != nil {
		return nil, 0, fmt.Errorf("FTS5 search: %w", err)
	}
	defer rows.Close()

	var results []map[string]interface{}
	for rows.Next() {
		var id, title, s, snippet string
		var rank float64
		if err := rows.Scan(&id, &title, &s, &rank, &snippet); err != nil {
			return nil, 0, fmt.Errorf("scanning search result: %w", err)
		}
		results = append(results, map[string]interface{}{
			"task_id":   id,
			"title":     title,
			"status":    s,
			"fts5_rank": rank,
			"snippet":   snippet,
		})
	}
	if results == nil {
		results = []map[string]interface{}{}
	}
	return results, len(results), nil
}

// ForceReady manually sets a task to ready status.
func ForceReady(database *sql.DB, taskID string, version int) (*Task, error) {
	return WithTxImmediateResult(database, func(conn *sql.Conn) (*Task, error) {
		ctx := context.Background()

		var hasReservation int
		conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM reservations WHERE task_id = ? AND active = 1", taskID).Scan(&hasReservation)
		if hasReservation > 0 {
			return nil, output.NewError(output.ErrTaskLocked,
				fmt.Sprintf("task %s has an active reservation", taskID),
				false, "Wait for agent to finish, or use solo task recover")
		}

		result, err := conn.ExecContext(ctx, `UPDATE tasks SET status = 'ready', version = version + 1 WHERE id = ? AND version = ?`,
			taskID, version)
		if err != nil {
			return nil, fmt.Errorf("updating task: %w", err)
		}
		rows, _ := result.RowsAffected()
		if rows == 0 {
			var exists int
			conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM tasks WHERE id = ?", taskID).Scan(&exists)
			if exists == 0 {
				return nil, output.NewError(output.ErrTaskNotFound, fmt.Sprintf("task %q not found", taskID), false, "")
			}
			return nil, output.NewError(output.ErrVersionConflict, "version mismatch", true, "Re-read current version, retry")
		}

		conn.ExecContext(ctx, `INSERT INTO audit_events (task_id, event_type, actor_type, actor_id) VALUES (?, 'task.force_ready', 'system', 'cli')`, taskID)

		task, err := scanTask(conn.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM tasks WHERE id = ?`, taskID))
		if err != nil {
			return nil, err
		}

		return task, nil
	})
}
