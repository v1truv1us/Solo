package cli

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/v1truv1us/solo/internal/db"
	"github.com/v1truv1us/solo/internal/output"
)

// App holds the CLI application state.
type App struct {
	DB       *sql.DB
	RepoRoot string
	DBPath   string
	JSON     bool
	Verbose  bool
}

// NewApp creates a new App, finding the repo root and opening the database.
// For `solo init`, pass skipDB=true.
func NewApp(dbOverride string, jsonOutput, verbose, skipDB bool) (*App, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getting cwd: %w", err)
	}

	// Find repo root using os.Stat per invariant #6
	repoRoot, err := db.FindRepoRoot(cwd)
	if err != nil {
		return nil, output.NewError(output.ErrNotARepo,
			"not inside a git repository", false, "Run inside a git repo")
	}

	app := &App{
		RepoRoot: repoRoot,
		JSON:     jsonOutput,
		Verbose:  verbose,
	}

	if dbOverride != "" {
		app.DBPath = dbOverride
	} else {
		app.DBPath = filepath.Join(repoRoot, ".solo", "solo.db")
	}

	if !skipDB {
		// Check .solo directory exists
		soloDir := filepath.Dir(app.DBPath)
		if _, err := os.Stat(soloDir); os.IsNotExist(err) {
			return nil, output.NewError(output.ErrNotARepo,
				"Solo not initialized. Run 'solo init' first.", false, "Run solo init")
		}

		database, err := db.OpenDB(app.DBPath)
		if err != nil {
			return nil, fmt.Errorf("opening database: %w", err)
		}
		app.DB = database

		// Run lazy zombie scan on every invocation per spec §2
		db.LazyZombieScan(database)
	}

	return app, nil
}

// Close closes the database connection.
func (a *App) Close() {
	if a.DB != nil {
		a.DB.Close()
	}
}

// OutputSuccess outputs a success response.
func (a *App) OutputSuccess(data interface{}) {
	if a.JSON {
		output.PrintJSON(data)
	} else {
		output.PrintJSON(data) // JSON for now; human-readable can be added later
	}
}

// OutputError outputs an error response and exits.
func (a *App) OutputError(err error) {
	if soloErr, ok := err.(*output.SoloError); ok {
		if a.JSON {
			output.PrintError(soloErr)
		} else {
			output.PrintErrorText("Error [%s]: %s\n", soloErr.Code, soloErr.Message)
		}
	} else {
		se := output.NewError(output.ErrDBError, err.Error(), false, "")
		if a.JSON {
			output.PrintError(se)
		} else {
			output.PrintErrorText("Error: %s\n", err.Error())
		}
	}
	os.Exit(1)
}
