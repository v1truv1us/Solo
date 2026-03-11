package solo

import (
	"database/sql"
	"strings"
)

func usesLegacyTaskStatusSchema(db *sql.DB) bool {
	var sqlDef string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='tasks'`).Scan(&sqlDef); err != nil {
		return false
	}
	s := strings.ToLower(sqlDef)
	return strings.Contains(s, "'open'") && strings.Contains(s, "'triaged'") && strings.Contains(s, "'in_progress'")
}

func taskStatusForWrite(db *sql.DB, canonical string) string {
	if !usesLegacyTaskStatusSchema(db) {
		return canonical
	}
	switch canonical {
	case "draft":
		return "open"
	case "active":
		return "in_progress"
	case "completed":
		return "done"
	case "failed":
		return "cancelled"
	default:
		return canonical
	}
}
