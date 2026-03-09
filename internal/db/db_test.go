package db_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/v1truv1us/solo/internal/db"
)

// testDB creates a temporary database for testing.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	database, err := db.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	if err := db.InitSchema(database, "test-machine"); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func TestOpenDBAndPragmas(t *testing.T) {
	database := testDB(t)

	// Verify PRAGMAs
	var journalMode string
	database.QueryRow("PRAGMA journal_mode").Scan(&journalMode)
	if journalMode != "wal" {
		t.Errorf("expected WAL mode, got %q", journalMode)
	}

	var fk int
	database.QueryRow("PRAGMA foreign_keys").Scan(&fk)
	if fk != 1 {
		t.Error("expected foreign_keys=ON")
	}

	var busyTimeout int
	database.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout)
	if busyTimeout != 5000 {
		t.Errorf("expected busy_timeout=5000, got %d", busyTimeout)
	}

	var sync string
	database.QueryRow("PRAGMA synchronous").Scan(&sync)
	// synchronous=NORMAL is 1
	if sync != "1" {
		t.Errorf("expected synchronous=1, got %q", sync)
	}
}

func TestInitSchemaIdempotent(t *testing.T) {
	database := testDB(t)

	// Second call should not error
	if err := db.InitSchema(database, "test-machine"); err != nil {
		t.Errorf("second InitSchema: %v", err)
	}
}

func TestSchemaIntegrity(t *testing.T) {
	database := testDB(t)

	var integrity string
	database.QueryRow("PRAGMA integrity_check").Scan(&integrity)
	if integrity != "ok" {
		t.Errorf("integrity check: %s", integrity)
	}
}

func TestFindRepoRoot(t *testing.T) {
	dir := t.TempDir()

	// No .git → error
	_, err := db.FindRepoRoot(dir)
	if err == nil {
		t.Error("expected error when no .git found")
	}

	// Create .git directory
	gitDir := filepath.Join(dir, ".git")
	os.Mkdir(gitDir, 0755)

	root, err := db.FindRepoRoot(dir)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if root != dir {
		t.Errorf("expected %q, got %q", dir, root)
	}

	// Find from subdirectory
	sub := filepath.Join(dir, "a", "b", "c")
	os.MkdirAll(sub, 0755)
	root, err = db.FindRepoRoot(sub)
	if err != nil {
		t.Errorf("unexpected error from subdir: %v", err)
	}
	if root != dir {
		t.Errorf("expected %q, got %q", dir, root)
	}
}

func TestGetConfig(t *testing.T) {
	database := testDB(t)

	val, err := db.GetConfig(database, "machine_id")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if val != "test-machine" {
		t.Errorf("expected test-machine, got %q", val)
	}

	_, err = db.GetConfig(database, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent key")
	}
}
