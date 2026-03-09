package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/v1truv1us/solo/internal/cli"
	solocontext "github.com/v1truv1us/solo/internal/context"
	"github.com/v1truv1us/solo/internal/db"
	"github.com/v1truv1us/solo/internal/git"
	"github.com/v1truv1us/solo/internal/output"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	// Global flags
	var jsonFlag bool
	var dbFlag string
	var verboseFlag bool

	// Find and strip global flags from args
	args := os.Args[1:]
	var cleanArgs []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonFlag = true
		case "--db":
			if i+1 < len(args) {
				dbFlag = args[i+1]
				i++
			}
		case "--verbose":
			verboseFlag = true
		default:
			cleanArgs = append(cleanArgs, args[i])
		}
	}

	if len(cleanArgs) == 0 {
		printUsage()
		os.Exit(1)
	}

	command := cleanArgs[0]
	subArgs := cleanArgs[1:]

	switch command {
	case "init":
		handleInit(subArgs, dbFlag, jsonFlag)
	case "health":
		handleHealth(dbFlag, jsonFlag, verboseFlag)
	case "search":
		handleSearch(subArgs, dbFlag, jsonFlag, verboseFlag)
	case "task":
		handleTask(subArgs, dbFlag, jsonFlag, verboseFlag)
	case "session":
		handleSession(subArgs, dbFlag, jsonFlag, verboseFlag)
	case "handoff":
		handleHandoff(subArgs, dbFlag, jsonFlag, verboseFlag)
	case "worktree":
		handleWorktree(subArgs, dbFlag, jsonFlag, verboseFlag)
	case "recover":
		handleRecover(subArgs, dbFlag, jsonFlag, verboseFlag)
	case "reservation":
		handleReservation(subArgs, dbFlag, jsonFlag, verboseFlag)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `Usage: solo <command> [flags]

Commands:
  init          Initialize Solo in the current repository
  health        Check system health
  search        Full-text search across tasks
  task          Task management (create, list, show, update, ready, deps, context, recover)
  session       Session management (start, end, list)
  handoff       Handoff management (create, list, show)
  worktree      Worktree management (list, inspect, cleanup)
  recover       Recovery operations (--all)
  reservation   Reservation operations (renew)

Global flags:
  --json        Output structured JSON
  --db <path>   Override database path
  --verbose     Include diagnostic output
`)
}

func handleInit(args []string, dbFlag string, jsonFlag bool) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	machineID := fs.String("machine-id", "", "Machine identifier")
	fs.Parse(args)
	cli.InitCmd(dbFlag, *machineID, jsonFlag)
}

func handleHealth(dbFlag string, jsonFlag, verbose bool) {
	app, err := cli.NewApp(dbFlag, jsonFlag, verbose, false)
	if err != nil {
		outputError(err, jsonFlag)
		return
	}
	defer app.Close()
	cli.HealthCmd(app)
}

func handleSearch(args []string, dbFlag string, jsonFlag, verbose bool) {
	app, err := cli.NewApp(dbFlag, jsonFlag, verbose, false)
	if err != nil {
		outputError(err, jsonFlag)
		return
	}
	defer app.Close()

	fs := flag.NewFlagSet("search", flag.ExitOnError)
	status := fs.String("status", "", "Filter by status")
	limit := fs.Int("limit", 10, "Max results")
	fs.Parse(args)

	query := strings.Join(fs.Args(), " ")
	if query == "" {
		outputError(output.NewError(output.ErrInvalidArgument, "search query is required", false, ""), jsonFlag)
		return
	}

	results, total, err := db.SearchTasks(app.DB, query, *status, *limit)
	if err != nil {
		outputError(err, jsonFlag)
		return
	}

	app.OutputSuccess(map[string]interface{}{
		"results": results,
		"total":   total,
	})
}

func handleTask(args []string, dbFlag string, jsonFlag, verbose bool) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: solo task <create|list|show|update|ready|deps|context|recover>\n")
		os.Exit(1)
	}

	subCmd := args[0]
	subArgs := args[1:]

	switch subCmd {
	case "create":
		handleTaskCreate(subArgs, dbFlag, jsonFlag, verbose)
	case "list":
		handleTaskList(subArgs, dbFlag, jsonFlag, verbose)
	case "show":
		handleTaskShow(subArgs, dbFlag, jsonFlag, verbose)
	case "update":
		handleTaskUpdate(subArgs, dbFlag, jsonFlag, verbose)
	case "ready":
		handleTaskReady(subArgs, dbFlag, jsonFlag, verbose)
	case "deps":
		handleTaskDeps(subArgs, dbFlag, jsonFlag, verbose)
	case "context":
		handleTaskContext(subArgs, dbFlag, jsonFlag, verbose)
	case "recover":
		handleTaskRecover(subArgs, dbFlag, jsonFlag, verbose)
	default:
		fmt.Fprintf(os.Stderr, "Unknown task subcommand: %s\n", subCmd)
		os.Exit(1)
	}
}

func handleTaskCreate(args []string, dbFlag string, jsonFlag, verbose bool) {
	app, err := cli.NewApp(dbFlag, jsonFlag, verbose, false)
	if err != nil {
		outputError(err, jsonFlag)
		return
	}
	defer app.Close()

	fs := flag.NewFlagSet("task create", flag.ExitOnError)
	title := fs.String("title", "", "Task title (required)")
	desc := fs.String("description", "", "Description")
	taskType := fs.String("type", "task", "Type: task, bug, feature, chore, spike")
	priority := fs.Int("priority", 3, "Priority 1-5")
	ac := fs.String("acceptance-criteria", "", "Acceptance criteria")
	dod := fs.String("definition-of-done", "", "Definition of done")
	parent := fs.String("parent", "", "Parent task ID")
	labelsStr := fs.String("labels", "", "Comma-separated labels")
	affectedStr := fs.String("affected-files", "", "Comma-separated affected files")
	depsStr := fs.String("deps", "", "Comma-separated dependency task IDs")
	fs.Parse(args)

	if *title == "" {
		outputError(output.NewError(output.ErrInvalidArgument, "--title is required", false, ""), jsonFlag)
		return
	}

	var labels, affectedFiles, deps []string
	if *labelsStr != "" {
		labels = strings.Split(*labelsStr, ",")
	}
	if *affectedStr != "" {
		affectedFiles = strings.Split(*affectedStr, ",")
	}
	if *depsStr != "" {
		deps = strings.Split(*depsStr, ",")
	}

	var parentPtr *string
	if *parent != "" {
		parentPtr = parent
	}

	task, err := db.CreateTask(app.DB, db.CreateTaskParams{
		Title:              *title,
		Description:        *desc,
		Type:               *taskType,
		Priority:           *priority,
		AcceptanceCriteria: *ac,
		DefinitionOfDone:   *dod,
		AffectedFiles:      affectedFiles,
		Labels:             labels,
		ParentTask:         parentPtr,
		Dependencies:       deps,
	})
	if err != nil {
		outputError(err, jsonFlag)
		return
	}

	app.OutputSuccess(map[string]interface{}{
		"task": task,
	})
}

func handleTaskList(args []string, dbFlag string, jsonFlag, verbose bool) {
	app, err := cli.NewApp(dbFlag, jsonFlag, verbose, false)
	if err != nil {
		outputError(err, jsonFlag)
		return
	}
	defer app.Close()

	fs := flag.NewFlagSet("task list", flag.ExitOnError)
	status := fs.String("status", "", "Filter by status")
	label := fs.String("label", "", "Filter by label")
	available := fs.Bool("available", false, "Only available tasks")
	limit := fs.Int("limit", 20, "Max results")
	offset := fs.Int("offset", 0, "Pagination offset")
	fs.Parse(args)

	result, err := db.ListTasks(app.DB, db.ListTasksParams{
		Status:    *status,
		Label:     *label,
		Available: *available,
		Limit:     *limit,
		Offset:    *offset,
	})
	if err != nil {
		outputError(err, jsonFlag)
		return
	}

	app.OutputSuccess(result)
}

func handleTaskShow(args []string, dbFlag string, jsonFlag, verbose bool) {
	app, err := cli.NewApp(dbFlag, jsonFlag, verbose, false)
	if err != nil {
		outputError(err, jsonFlag)
		return
	}
	defer app.Close()

	if len(args) == 0 {
		outputError(output.NewError(output.ErrInvalidArgument, "task ID is required", false, ""), jsonFlag)
		return
	}

	taskID := args[0]
	task, err := db.GetTask(app.DB, taskID)
	if err != nil {
		outputError(err, jsonFlag)
		return
	}

	// Get dependencies
	deps, _ := db.GetTaskDependencies(app.DB, taskID)

	// Get active reservation
	res, _ := db.GetActiveReservation(app.DB, taskID)

	// Get session count
	var sessionCount int
	app.DB.QueryRow("SELECT COUNT(*) FROM sessions WHERE task_id = ?", taskID).Scan(&sessionCount)

	result := map[string]interface{}{
		"task":         task,
		"dependencies": deps,
		"session_count": sessionCount,
	}
	if res != nil {
		result["active_reservation"] = map[string]interface{}{
			"id":         res.ID,
			"worker_id":  res.WorkerID,
			"expires_at": res.ExpiresAt,
		}
	} else {
		result["active_reservation"] = nil
	}

	app.OutputSuccess(result)
}

func handleTaskUpdate(args []string, dbFlag string, jsonFlag, verbose bool) {
	app, err := cli.NewApp(dbFlag, jsonFlag, verbose, false)
	if err != nil {
		outputError(err, jsonFlag)
		return
	}
	defer app.Close()

	if len(args) == 0 {
		outputError(output.NewError(output.ErrInvalidArgument, "task ID is required", false, ""), jsonFlag)
		return
	}

	taskID := args[0]
	fs := flag.NewFlagSet("task update", flag.ExitOnError)
	title := fs.String("title", "", "New title")
	desc := fs.String("description", "", "New description")
	taskType := fs.String("type", "", "New type")
	status := fs.String("status", "", "New status")
	priority := fs.String("priority", "", "New priority")
	version := fs.Int("version", 0, "OCC version (required)")
	labelsStr := fs.String("labels", "", "New labels (comma-separated)")
	ac := fs.String("acceptance-criteria", "", "Acceptance criteria")
	dod := fs.String("definition-of-done", "", "Definition of done")
	fs.Parse(args[1:])

	if *version == 0 {
		outputError(output.NewError(output.ErrInvalidArgument, "--version is required for updates", false, ""), jsonFlag)
		return
	}

	params := db.UpdateTaskParams{
		TaskID:  taskID,
		Version: *version,
	}

	if *title != "" {
		params.Title = title
	}
	if *desc != "" {
		params.Description = desc
	}
	if *taskType != "" {
		params.Type = taskType
	}
	if *status != "" {
		params.Status = status
	}
	if *priority != "" {
		p, err := strconv.Atoi(*priority)
		if err != nil {
			outputError(output.NewError(output.ErrInvalidArgument, "priority must be a number", false, ""), jsonFlag)
			return
		}
		params.Priority = &p
	}
	if *labelsStr != "" {
		labels := strings.Split(*labelsStr, ",")
		params.Labels = &labels
	}
	if *ac != "" {
		params.AcceptanceCriteria = ac
	}
	if *dod != "" {
		params.DefinitionOfDone = dod
	}

	task, err := db.UpdateTask(app.DB, params)
	if err != nil {
		outputError(err, jsonFlag)
		return
	}

	app.OutputSuccess(map[string]interface{}{"task": task})
}

func handleTaskReady(args []string, dbFlag string, jsonFlag, verbose bool) {
	app, err := cli.NewApp(dbFlag, jsonFlag, verbose, false)
	if err != nil {
		outputError(err, jsonFlag)
		return
	}
	defer app.Close()

	if len(args) == 0 {
		outputError(output.NewError(output.ErrInvalidArgument, "task ID is required", false, ""), jsonFlag)
		return
	}

	taskID := args[0]
	fs := flag.NewFlagSet("task ready", flag.ExitOnError)
	version := fs.Int("version", 0, "OCC version (required)")
	fs.Parse(args[1:])

	if *version == 0 {
		outputError(output.NewError(output.ErrInvalidArgument, "--version is required", false, ""), jsonFlag)
		return
	}

	task, err := db.ForceReady(app.DB, taskID, *version)
	if err != nil {
		outputError(err, jsonFlag)
		return
	}

	app.OutputSuccess(map[string]interface{}{"task": task})
}

func handleTaskDeps(args []string, dbFlag string, jsonFlag, verbose bool) {
	app, err := cli.NewApp(dbFlag, jsonFlag, verbose, false)
	if err != nil {
		outputError(err, jsonFlag)
		return
	}
	defer app.Close()

	if len(args) == 0 {
		outputError(output.NewError(output.ErrInvalidArgument, "task ID is required", false, ""), jsonFlag)
		return
	}

	taskID := args[0]
	deps, err := db.GetTaskDependencies(app.DB, taskID)
	if err != nil {
		outputError(err, jsonFlag)
		return
	}

	// Find blocking deps
	var blocking []string
	for _, d := range deps {
		if d.Status != "done" {
			blocking = append(blocking, d.ID)
		}
	}
	if blocking == nil {
		blocking = []string{}
	}

	app.OutputSuccess(map[string]interface{}{
		"task_id":      taskID,
		"dependencies": deps,
		"blocking":     blocking,
	})
}

func handleTaskContext(args []string, dbFlag string, jsonFlag, verbose bool) {
	app, err := cli.NewApp(dbFlag, jsonFlag, verbose, false)
	if err != nil {
		outputError(err, jsonFlag)
		return
	}
	defer app.Close()

	if len(args) == 0 {
		outputError(output.NewError(output.ErrInvalidArgument, "task ID is required", false, ""), jsonFlag)
		return
	}

	taskID := args[0]
	fs := flag.NewFlagSet("task context", flag.ExitOnError)
	maxTokens := fs.Int("max-tokens", 0, "Token budget")
	fs.Parse(args[1:])

	bundle, err := solocontext.AssembleBundle(app.DB, taskID, *maxTokens)
	if err != nil {
		outputError(err, jsonFlag)
		return
	}

	app.OutputSuccess(bundle)
}

func handleTaskRecover(args []string, dbFlag string, jsonFlag, verbose bool) {
	app, err := cli.NewApp(dbFlag, jsonFlag, verbose, false)
	if err != nil {
		outputError(err, jsonFlag)
		return
	}
	defer app.Close()

	if len(args) == 0 {
		outputError(output.NewError(output.ErrInvalidArgument, "task ID is required", false, ""), jsonFlag)
		return
	}

	taskID := args[0]
	fs := flag.NewFlagSet("task recover", flag.ExitOnError)
	version := fs.Int("version", 0, "OCC version (required)")
	fs.Parse(args[1:])

	if *version == 0 {
		outputError(output.NewError(output.ErrInvalidArgument, "--version is required", false, ""), jsonFlag)
		return
	}

	result, err := db.RecoverTask(app.DB, taskID, *version, nil, nil)
	if err != nil {
		outputError(err, jsonFlag)
		return
	}

	app.OutputSuccess(result)
}

func handleSession(args []string, dbFlag string, jsonFlag, verbose bool) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: solo session <start|end|list>\n")
		os.Exit(1)
	}

	switch args[0] {
	case "start":
		handleSessionStart(args[1:], dbFlag, jsonFlag, verbose)
	case "end":
		handleSessionEnd(args[1:], dbFlag, jsonFlag, verbose)
	case "list":
		handleSessionList(args[1:], dbFlag, jsonFlag, verbose)
	default:
		fmt.Fprintf(os.Stderr, "Unknown session subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

func handleSessionStart(args []string, dbFlag string, jsonFlag, verbose bool) {
	app, err := cli.NewApp(dbFlag, jsonFlag, verbose, false)
	if err != nil {
		outputError(err, jsonFlag)
		return
	}
	defer app.Close()

	if len(args) == 0 {
		outputError(output.NewError(output.ErrInvalidArgument, "task ID is required", false, ""), jsonFlag)
		return
	}

	taskID := args[0]
	fs := flag.NewFlagSet("session start", flag.ExitOnError)
	worker := fs.String("worker", "", "Worker identifier (required)")
	ttl := fs.Int("ttl", 0, "Reservation TTL in seconds")
	pid := fs.Int("pid", 0, "Agent PID (default: current process)")
	fs.Parse(args[1:])

	if *worker == "" {
		outputError(output.NewError(output.ErrInvalidArgument, "--worker is required", false, ""), jsonFlag)
		return
	}

	result, err := db.StartSession(app.DB, taskID, *worker, *pid, *ttl)
	if err != nil {
		outputError(err, jsonFlag)
		return
	}

	// Create worktree (after DB transaction commits per spec §6.2)
	wtPath, branch, wtErr := git.CreateWorktree(app.DB, app.RepoRoot, taskID, result.SessionID, result.ReservationID)
	if wtErr != nil {
		// Compensating transaction to undo session start
		db.CompensateSessionStart(app.DB, result.SessionID, result.ReservationID, taskID, result.TaskVersion-1)
		outputError(wtErr, jsonFlag)
		return
	}

	result.WorktreePath = wtPath
	result.Branch = branch

	// Assemble context bundle
	bundle, _ := solocontext.AssembleBundle(app.DB, taskID, 0)

	resp := map[string]interface{}{
		"session_id":     result.SessionID,
		"reservation_id": result.ReservationID,
		"worktree_path":  result.WorktreePath,
		"branch":         result.Branch,
		"expires_at":     result.ExpiresAt,
	}
	if bundle != nil {
		resp["context"] = bundle
	}

	app.OutputSuccess(resp)
}

func handleSessionEnd(args []string, dbFlag string, jsonFlag, verbose bool) {
	app, err := cli.NewApp(dbFlag, jsonFlag, verbose, false)
	if err != nil {
		outputError(err, jsonFlag)
		return
	}
	defer app.Close()

	if len(args) == 0 {
		outputError(output.NewError(output.ErrInvalidArgument, "task ID is required", false, ""), jsonFlag)
		return
	}

	taskID := args[0]
	fs := flag.NewFlagSet("session end", flag.ExitOnError)
	result := fs.String("result", "", "Result: completed, failed, interrupted, abandoned (required)")
	notes := fs.String("notes", "", "Session notes")
	summary := fs.String("summary", "", "Session summary (alias for notes)")
	commits := fs.String("commits", "", "Comma-separated commit SHAs")
	statusOverride := fs.String("status", "", "Override task status (e.g. 'done')")
	fs.Parse(args[1:])

	if *result == "" {
		outputError(output.NewError(output.ErrInvalidArgument, "--result is required", false, ""), jsonFlag)
		return
	}

	notesVal := *notes
	if notesVal == "" {
		notesVal = *summary
	}

	var commitsJSON string
	if *commits != "" {
		shas := strings.Split(*commits, ",")
		var commitObjs []map[string]string
		for _, sha := range shas {
			commitObjs = append(commitObjs, map[string]string{"sha": strings.TrimSpace(sha)})
		}
		b, _ := json.Marshal(commitObjs)
		commitsJSON = string(b)
	}

	endResult, err := db.EndSession(app.DB, db.EndSessionParams{
		TaskID:         taskID,
		Result:         *result,
		Notes:          notesVal,
		Commits:        commitsJSON,
		StatusOverride: *statusOverride,
	})
	if err != nil {
		outputError(err, jsonFlag)
		return
	}

	app.OutputSuccess(endResult)
}

func handleSessionList(args []string, dbFlag string, jsonFlag, verbose bool) {
	app, err := cli.NewApp(dbFlag, jsonFlag, verbose, false)
	if err != nil {
		outputError(err, jsonFlag)
		return
	}
	defer app.Close()

	fs := flag.NewFlagSet("session list", flag.ExitOnError)
	taskID := fs.String("task", "", "Filter by task ID")
	worker := fs.String("worker", "", "Filter by worker")
	active := fs.Bool("active", false, "Only active sessions")
	fs.Parse(args)

	sessions, err := db.ListSessions(app.DB, *taskID, *worker, *active)
	if err != nil {
		outputError(err, jsonFlag)
		return
	}

	app.OutputSuccess(map[string]interface{}{
		"sessions": sessions,
	})
}

func handleHandoff(args []string, dbFlag string, jsonFlag, verbose bool) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: solo handoff <create|list|show>\n")
		os.Exit(1)
	}

	switch args[0] {
	case "create":
		handleHandoffCreate(args[1:], dbFlag, jsonFlag, verbose)
	case "list":
		handleHandoffList(args[1:], dbFlag, jsonFlag, verbose)
	case "show":
		handleHandoffShow(args[1:], dbFlag, jsonFlag, verbose)
	default:
		fmt.Fprintf(os.Stderr, "Unknown handoff subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

func handleHandoffCreate(args []string, dbFlag string, jsonFlag, verbose bool) {
	app, err := cli.NewApp(dbFlag, jsonFlag, verbose, false)
	if err != nil {
		outputError(err, jsonFlag)
		return
	}
	defer app.Close()

	if len(args) == 0 {
		outputError(output.NewError(output.ErrInvalidArgument, "task ID is required", false, ""), jsonFlag)
		return
	}

	taskID := args[0]
	fs := flag.NewFlagSet("handoff create", flag.ExitOnError)
	summary := fs.String("summary", "", "Summary (required)")
	remainingWork := fs.String("remaining-work", "", "Remaining work")
	toWorker := fs.String("to", "", "Recommended next worker")
	filesStr := fs.String("files", "", "Comma-separated modified files")
	fs.Parse(args[1:])

	if *summary == "" {
		outputError(output.NewError(output.ErrInvalidArgument, "--summary is required", false, ""), jsonFlag)
		return
	}

	var files []string
	if *filesStr != "" {
		files = strings.Split(*filesStr, ",")
	}

	result, err := db.CreateHandoff(app.DB, db.CreateHandoffParams{
		TaskID:        taskID,
		Summary:       *summary,
		RemainingWork: *remainingWork,
		ToWorker:      *toWorker,
		Files:         files,
	})
	if err != nil {
		outputError(err, jsonFlag)
		return
	}

	app.OutputSuccess(result)
}

func handleHandoffList(args []string, dbFlag string, jsonFlag, verbose bool) {
	app, err := cli.NewApp(dbFlag, jsonFlag, verbose, false)
	if err != nil {
		outputError(err, jsonFlag)
		return
	}
	defer app.Close()

	fs := flag.NewFlagSet("handoff list", flag.ExitOnError)
	taskID := fs.String("task", "", "Filter by task ID")
	status := fs.String("status", "", "Filter by status")
	fs.Parse(args)

	handoffs, err := db.ListHandoffs(app.DB, *taskID, *status)
	if err != nil {
		outputError(err, jsonFlag)
		return
	}

	app.OutputSuccess(map[string]interface{}{
		"handoffs": handoffs,
	})
}

func handleHandoffShow(args []string, dbFlag string, jsonFlag, verbose bool) {
	app, err := cli.NewApp(dbFlag, jsonFlag, verbose, false)
	if err != nil {
		outputError(err, jsonFlag)
		return
	}
	defer app.Close()

	if len(args) == 0 {
		outputError(output.NewError(output.ErrInvalidArgument, "handoff ID is required", false, ""), jsonFlag)
		return
	}

	handoff, err := db.GetHandoff(app.DB, args[0])
	if err != nil {
		outputError(err, jsonFlag)
		return
	}

	app.OutputSuccess(handoff)
}

func handleWorktree(args []string, dbFlag string, jsonFlag, verbose bool) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: solo worktree <list|inspect|cleanup>\n")
		os.Exit(1)
	}

	switch args[0] {
	case "list":
		handleWorktreeList(args[1:], dbFlag, jsonFlag, verbose)
	case "inspect":
		handleWorktreeInspect(args[1:], dbFlag, jsonFlag, verbose)
	case "cleanup":
		handleWorktreeCleanup(args[1:], dbFlag, jsonFlag, verbose)
	default:
		fmt.Fprintf(os.Stderr, "Unknown worktree subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

func handleWorktreeList(args []string, dbFlag string, jsonFlag, verbose bool) {
	app, err := cli.NewApp(dbFlag, jsonFlag, verbose, false)
	if err != nil {
		outputError(err, jsonFlag)
		return
	}
	defer app.Close()

	fs := flag.NewFlagSet("worktree list", flag.ExitOnError)
	status := fs.String("status", "", "Filter by status")
	fs.Parse(args)

	worktrees, err := git.ListWorktrees(app.DB, *status)
	if err != nil {
		outputError(err, jsonFlag)
		return
	}

	maxWT := 5
	maxStr, _ := db.GetConfig(app.DB, "max_worktrees")
	fmt.Sscanf(maxStr, "%d", &maxWT)

	app.OutputSuccess(map[string]interface{}{
		"worktrees": worktrees,
		"total":     len(worktrees),
		"max":       maxWT,
	})
}

func handleWorktreeInspect(args []string, dbFlag string, jsonFlag, verbose bool) {
	app, err := cli.NewApp(dbFlag, jsonFlag, verbose, false)
	if err != nil {
		outputError(err, jsonFlag)
		return
	}
	defer app.Close()

	if len(args) == 0 {
		outputError(output.NewError(output.ErrInvalidArgument, "task ID or worktree ID is required", false, ""), jsonFlag)
		return
	}

	wt, err := git.InspectWorktree(app.DB, app.RepoRoot, args[0])
	if err != nil {
		outputError(err, jsonFlag)
		return
	}

	app.OutputSuccess(wt)
}

func handleWorktreeCleanup(args []string, dbFlag string, jsonFlag, verbose bool) {
	app, err := cli.NewApp(dbFlag, jsonFlag, verbose, false)
	if err != nil {
		outputError(err, jsonFlag)
		return
	}
	defer app.Close()

	fs := flag.NewFlagSet("worktree cleanup", flag.ExitOnError)
	force := fs.Bool("force", false, "Force cleanup of dirty worktrees")
	fs.Parse(args)

	taskID := ""
	if len(fs.Args()) > 0 {
		taskID = fs.Args()[0]
	}

	cleaned, skipped, err := git.CleanupWorktree(app.DB, app.RepoRoot, taskID, *force)
	if err != nil {
		outputError(err, jsonFlag)
		return
	}

	app.OutputSuccess(map[string]interface{}{
		"cleaned": cleaned,
		"skipped": skipped,
	})
}

func handleRecover(args []string, dbFlag string, jsonFlag, verbose bool) {
	app, err := cli.NewApp(dbFlag, jsonFlag, verbose, false)
	if err != nil {
		outputError(err, jsonFlag)
		return
	}
	defer app.Close()

	// solo recover --all
	result, err := db.RecoverAll(app.DB)
	if err != nil {
		outputError(err, jsonFlag)
		return
	}

	app.OutputSuccess(result)
}

func handleReservation(args []string, dbFlag string, jsonFlag, verbose bool) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: solo reservation <renew>\n")
		os.Exit(1)
	}

	switch args[0] {
	case "renew":
		handleReservationRenew(args[1:], dbFlag, jsonFlag, verbose)
	default:
		fmt.Fprintf(os.Stderr, "Unknown reservation subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

func handleReservationRenew(args []string, dbFlag string, jsonFlag, verbose bool) {
	app, err := cli.NewApp(dbFlag, jsonFlag, verbose, false)
	if err != nil {
		outputError(err, jsonFlag)
		return
	}
	defer app.Close()

	if len(args) == 0 {
		outputError(output.NewError(output.ErrInvalidArgument, "task ID is required", false, ""), jsonFlag)
		return
	}

	res, err := db.RenewReservation(app.DB, args[0])
	if err != nil {
		outputError(err, jsonFlag)
		return
	}

	app.OutputSuccess(map[string]interface{}{
		"reservation": map[string]interface{}{
			"id":              res.ID,
			"task_id":         res.TaskID,
			"new_expires_at":  res.ExpiresAt,
			"remaining_sec":   res.TTLSec,
		},
	})
}

func outputError(err error, jsonFlag bool) {
	if soloErr, ok := err.(*output.SoloError); ok {
		if jsonFlag {
			output.PrintError(soloErr)
		} else {
			fmt.Fprintf(os.Stderr, "Error [%s]: %s\n", soloErr.Code, soloErr.Message)
		}
	} else {
		se := &output.SoloError{Code: output.ErrDBError, Message: err.Error()}
		if jsonFlag {
			output.PrintError(se)
		} else {
			fmt.Fprintf(os.Stderr, "Error: %s\n", err.Error())
		}
	}
	os.Exit(1)
}
