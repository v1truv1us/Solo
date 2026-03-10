package solo

import (
	"database/sql"
	"encoding/json"
)

func (a *App) ListAudit(taskID string, limit, offset int) (map[string]any, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return a.withDB(func(db *sql.DB) (map[string]any, error) {
		q := `SELECT id, task_id, event_type, actor_type, actor_id, old_value_json, new_value_json, created_at FROM audit_events WHERE 1=1`
		args := []any{}
		if taskID != "" {
			q += ` AND task_id=?`
			args = append(args, taskID)
		}
		q += ` ORDER BY id DESC LIMIT ? OFFSET ?`
		args = append(args, limit, offset)
		rows, err := db.Query(q, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		events := []map[string]any{}
		for rows.Next() {
			event, err := scanAuditRow(rows)
			if err != nil {
				return nil, err
			}
			events = append(events, event)
		}
		return map[string]any{"events": events, "limit": limit, "offset": offset}, nil
	})
}

func (a *App) ShowAudit(id int) (map[string]any, error) {
	if id <= 0 {
		return nil, ErrInvalidArgument("invalid event id")
	}
	return a.withDB(func(db *sql.DB) (map[string]any, error) {
		rows, err := db.Query(`SELECT id, task_id, event_type, actor_type, actor_id, old_value_json, new_value_json, created_at FROM audit_events WHERE id=?`, id)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		if !rows.Next() {
			return nil, errWith("AUDIT_NOT_FOUND", "Audit event not found", false, "")
		}
		event, err := scanAuditRow(rows)
		if err != nil {
			return nil, err
		}
		return map[string]any{"event": event}, nil
	})
}

func scanAuditRow(rows *sql.Rows) (map[string]any, error) {
	var id int
	var taskID, eventType, actorType, actorID, oldJSON, newJSON, createdAt string
	if err := rows.Scan(&id, &taskID, &eventType, &actorType, &actorID, &oldJSON, &newJSON, &createdAt); err != nil {
		return nil, err
	}
	oldVal := any(nil)
	if oldJSON != "" && oldJSON != "null" {
		if err := json.Unmarshal([]byte(oldJSON), &oldVal); err != nil {
			return nil, err
		}
	}
	newVal := any(nil)
	if newJSON != "" && newJSON != "null" {
		if err := json.Unmarshal([]byte(newJSON), &newVal); err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"id": id, "task_id": taskID, "event_type": eventType, "actor_type": actorType,
		"actor_id": actorID, "old_value": oldVal, "new_value": newVal, "created_at": createdAt,
	}, nil
}
