package solo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

type App struct{}

func NewApp() *App { return &App{} }

func (a *App) Init(machineID, skillScope, skillAgent string, installSkill bool) (map[string]any, error) {
	root, err := discoverRepoRoot(".")
	if err != nil {
		return nil, err
	}
	soloDir := filepath.Join(root, ".solo")
	dbPath := filepath.Join(soloDir, "solo.db")
	alreadyInitialized := false
	if _, err := os.Stat(dbPath); err == nil {
		alreadyInitialized = true
	}
	if err := os.MkdirAll(soloDir, 0o755); err != nil {
		return nil, err
	}
	if !alreadyInitialized {
		db, err := openDB(dbPath)
		if err != nil {
			return nil, err
		}
		defer db.Close()
		if err := applySchema(db); err != nil {
			return nil, err
		}
		if machineID == "" {
			machineID, _ = os.Hostname()
			if machineID == "" {
				machineID = "default"
			}
		}
		if err := setDefaultConfig(db, machineID); err != nil {
			return nil, err
		}
	} else if !installSkill {
		return nil, errAlreadyInitialized()
	}

	if machineID == "" {
		machineID, _ = os.Hostname()
		if machineID == "" {
			machineID = "default"
		}
	}
	resp := map[string]any{
		"initialized":    !alreadyInitialized,
		"database":       dbPath,
		"machine_id":     machineID,
		"schema_version": 2,
	}
	if installSkill {
		path, err := installSoloSkill(root, skillScope, skillAgent)
		if err != nil {
			return nil, err
		}
		resp["skill"] = skillInstallSummary(skillScope, path)
	}
	if alreadyInitialized {
		resp["already_initialized"] = true
	}
	return resp, nil
}

func (a *App) withDB(op func(*sql.DB) (map[string]any, error)) (map[string]any, error) {
	root, err := discoverRepoRoot(".")
	if err != nil {
		return nil, err
	}
	dbPath := filepath.Join(root, ".solo", "solo.db")
	db, err := openDB(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err := applySchema(db); err != nil {
		return nil, err
	}
	lazyZombieScan(db)
	return op(db)
}

func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	pragmas := []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA cache_size = -64000",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, err
		}
	}
	return db, nil
}

func applyConnPragmas(ctx context.Context, conn *sql.Conn, busyTimeout int) error {
	pragmas := []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA foreign_keys = ON",
		fmt.Sprintf("PRAGMA busy_timeout = %d", busyTimeout),
		"PRAGMA synchronous = NORMAL",
		"PRAGMA cache_size = -64000",
	}
	for _, p := range pragmas {
		if _, err := conn.ExecContext(ctx, p); err != nil {
			return err
		}
	}
	return nil
}

func withImmediateTx(ctx context.Context, db *sql.DB, fn func(*sql.Conn) error) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "busy") {
			return errSQLiteBusy()
		}
		return err
	}
	defer conn.Close()
	if err := applyConnPragmas(ctx, conn, 5000); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "busy") {
			return errSQLiteBusy()
		}
		return err
	}
	if err := fn(conn); err != nil {
		_, _ = conn.ExecContext(ctx, "ROLLBACK")
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		_, _ = conn.ExecContext(ctx, "ROLLBACK")
		return err
	}
	return nil
}

func nowISO() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}

func parseJSONList[T any](s string) ([]T, error) {
	if strings.TrimSpace(s) == "" {
		return []T{}, nil
	}
	var out []T
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func configInt(db *sql.DB, key string, def int) int {
	var s string
	if err := db.QueryRow("SELECT value FROM config WHERE key=?", key).Scan(&s); err != nil {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}

func configString(db *sql.DB, key, def string) string {
	var s string
	if err := db.QueryRow("SELECT value FROM config WHERE key=?", key).Scan(&s); err != nil {
		return def
	}
	if s == "" {
		return def
	}
	return s
}

type sqlExecerContext interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func writeAudit(ctx context.Context, tx sqlExecerContext, taskID, eventType, actorType, actorID string, oldV, newV any) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO audit_events (task_id, event_type, actor_type, actor_id, old_value_json, new_value_json)
		VALUES (?, ?, ?, ?, ?, ?)`, taskID, eventType, actorType, actorID, mustJSON(oldV), mustJSON(newV))
	return err
}

func readTaskBasic(db *sql.DB, taskID string) (map[string]any, error) {
	row := db.QueryRow(`SELECT id,title,description,type,status,priority,acceptance_criteria,definition_of_done,
		affected_files,labels,parent_task,version,created_at,updated_at FROM tasks WHERE id=?`, taskID)
	var id, title, description, typ, status, ac, dod, affectedJSON, labelsJSON, created, updated string
	var priority, version int
	var parent sql.NullString
	if err := row.Scan(&id, &title, &description, &typ, &status, &priority, &ac, &dod, &affectedJSON, &labelsJSON, &parent, &version, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errTaskNotFound(taskID)
		}
		return nil, err
	}
	affected, err := parseJSONList[string](affectedJSON)
	if err != nil {
		return nil, err
	}
	labels, err := parseJSONList[string](labelsJSON)
	if err != nil {
		return nil, err
	}
	var parentVal any
	if parent.Valid {
		parentVal = parent.String
	}
	status = canonicalTaskStatus(status)
	return map[string]any{
		"id": id, "title": title, "description": description, "type": typ, "status": status, "status_legacy": legacyTaskStatus(status),
		"priority": priorityLabel(priority), "priority_value": priority, "acceptance_criteria": ac, "definition_of_done": dod,
		"affected_files": affected, "labels": labels, "parent_task": parentVal,
		"version": version, "created_at": created, "updated_at": updated,
	}, nil
}

func randomID() string { return uuid.NewString() }

func sqlTimeAddSeconds(ttl int) string {
	if ttl <= 0 {
		ttl = 3600
	}
	return fmt.Sprintf("+%d seconds", ttl)
}
