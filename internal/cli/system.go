package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/v1truv1us/solo/internal/db"
	"github.com/v1truv1us/solo/internal/output"
)

// InitCmd handles `solo init`.
func InitCmd(dbOverride, machineID string, jsonOutput bool) {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}

	repoRoot, err := db.FindRepoRoot(cwd)
	if err != nil {
		if jsonOutput {
			output.PrintError(output.NewError(output.ErrNotARepo, "not inside a git repository", false, "Run inside a git repo"))
		} else {
			fmt.Fprintf(os.Stderr, "Error: not inside a git repository\n")
		}
		os.Exit(1)
	}

	dbPath := dbOverride
	if dbPath == "" {
		dbPath = filepath.Join(repoRoot, ".solo", "solo.db")
	}

	// Check if already initialized
	if _, err := os.Stat(dbPath); err == nil {
		if jsonOutput {
			output.PrintError(output.NewError(output.ErrAlreadyInitialized, ".solo/solo.db already exists", false, ""))
		} else {
			fmt.Fprintf(os.Stderr, "Error: Solo already initialized\n")
		}
		os.Exit(1)
	}

	// Create .solo directory
	soloDir := filepath.Dir(dbPath)
	if err := os.MkdirAll(soloDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating .solo directory: %s\n", err)
		os.Exit(1)
	}

	// Open and initialize database
	database, err := db.OpenDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening database: %s\n", err)
		os.Exit(1)
	}
	defer database.Close()

	if err := db.InitSchema(database, machineID); err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing schema: %s\n", err)
		os.Exit(1)
	}

	// Add .solo to .gitignore if not already there
	gitignorePath := filepath.Join(repoRoot, ".gitignore")
	addToGitignore(gitignorePath, ".solo/")

	result := map[string]interface{}{
		"initialized":    true,
		"database":       dbPath,
		"machine_id":     machineID,
		"schema_version": 1,
	}
	if machineID == "" {
		mid, _ := db.GetConfig(database, "machine_id")
		result["machine_id"] = mid
	}

	output.PrintJSON(result)
}

func addToGitignore(path, entry string) {
	content, err := os.ReadFile(path)
	if err == nil {
		// Check if already present
		for _, line := range splitLines(string(content)) {
			if line == entry {
				return
			}
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	if len(content) > 0 && content[len(content)-1] != '\n' {
		f.WriteString("\n")
	}
	f.WriteString(entry + "\n")
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			line := s[start:i]
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			lines = append(lines, line)
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// HealthCmd handles `solo health`.
func HealthCmd(app *App) {
	// Database info
	var dbSize int64
	fi, err := os.Stat(app.DBPath)
	if err == nil {
		dbSize = fi.Size()
	}

	// Integrity check
	var integrity string
	app.DB.QueryRow("PRAGMA integrity_check").Scan(&integrity)

	// Schema version
	var schemaVersion int
	app.DB.QueryRow("SELECT MAX(version) FROM schema_version").Scan(&schemaVersion)

	// Machine ID
	machineID, _ := db.GetConfig(app.DB, "machine_id")

	// Task counts by status
	taskCounts := map[string]int{}
	for _, status := range db.AllStatuses() {
		var count int
		app.DB.QueryRow("SELECT COUNT(*) FROM tasks WHERE status = ?", status).Scan(&count)
		taskCounts[status] = count
	}

	// Active reservations/sessions
	var activeRes, activeSess, zombieSess, expiredRes int
	app.DB.QueryRow("SELECT COUNT(*) FROM reservations WHERE active = 1").Scan(&activeRes)
	app.DB.QueryRow("SELECT COUNT(*) FROM sessions WHERE ended_at IS NULL").Scan(&activeSess)
	app.DB.QueryRow(`SELECT COUNT(*) FROM sessions s
		JOIN reservations r ON r.id = s.reservation_id
		WHERE s.ended_at IS NULL AND r.active = 1 AND r.expires_at < strftime('%Y-%m-%dT%H:%M:%fZ', 'now')`).Scan(&zombieSess)
	app.DB.QueryRow(`SELECT COUNT(*) FROM reservations WHERE active = 1 AND expires_at < strftime('%Y-%m-%dT%H:%M:%fZ', 'now')`).Scan(&expiredRes)

	// Worktree info
	var activeWT, pendingWT int
	app.DB.QueryRow("SELECT COUNT(*) FROM worktrees WHERE status = 'active'").Scan(&activeWT)
	app.DB.QueryRow("SELECT COUNT(*) FROM worktrees WHERE status = 'cleanup_pending'").Scan(&pendingWT)
	maxWT, _ := db.GetConfig(app.DB, "max_worktrees")

	var diskUsage int64
	app.DB.QueryRow("SELECT COALESCE(SUM(disk_usage_bytes), 0) FROM worktrees WHERE status = 'active'").Scan(&diskUsage)

	var pendingHandoffs int
	app.DB.QueryRow("SELECT COUNT(*) FROM handoffs WHERE status = 'pending'").Scan(&pendingHandoffs)

	maxWTInt := 5
	fmt.Sscanf(maxWT, "%d", &maxWTInt)

	result := map[string]interface{}{
		"database": map[string]interface{}{
			"path":       app.DBPath,
			"size_bytes": dbSize,
			"integrity":  integrity,
		},
		"schema_version":       schemaVersion,
		"machine_id":           machineID,
		"tasks":                taskCounts,
		"active_reservations":  activeRes,
		"active_sessions":      activeSess,
		"zombie_sessions":      zombieSess,
		"expired_reservations": expiredRes,
		"worktrees": map[string]interface{}{
			"active":          activeWT,
			"cleanup_pending": pendingWT,
			"max":             maxWTInt,
			"disk_usage_mb":   diskUsage / (1024 * 1024),
		},
		"pending_handoffs": pendingHandoffs,
		"issues":           []interface{}{},
	}

	app.OutputSuccess(result)
}
