package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"solo/internal/solo"
)

func main() {
	app := solo.NewApp()
	if err := run(app, os.Args[1:]); err != nil {
		var se *solo.Error
		if errors.As(err, &se) {
			_ = writeJSON(map[string]any{"ok": false, "error": se})
			os.Exit(1)
		}
		_ = writeJSON(map[string]any{"ok": false, "error": solo.NewInternalError(err)})
		os.Exit(1)
	}
}

func run(app *solo.App, args []string) error {
	if len(args) == 0 {
		return solo.ErrInvalidArgument("missing command")
	}
	jsonOut := hasFlag(args, "--json")
	_ = jsonOut

	switch args[0] {
	case "init":
		machineID := ""
		for i := 1; i < len(args); i++ {
			if args[i] == "--machine-id" && i+1 < len(args) {
				machineID = args[i+1]
				i++
			}
		}
		resp, err := app.Init(machineID)
		if err != nil {
			return err
		}
		return writeOK(resp)
	case "health":
		resp, err := app.Health()
		if err != nil {
			return err
		}
		return writeOK(resp)
	case "search":
		if len(args) < 2 {
			return solo.ErrInvalidArgument("missing query")
		}
		query := args[1]
		status := ""
		limit := 10
		for i := 2; i < len(args); i++ {
			switch args[i] {
			case "--status":
				if i+1 < len(args) {
					status = args[i+1]
					i++
				}
			case "--limit":
				if i+1 < len(args) {
					v, _ := strconv.Atoi(args[i+1])
					if v > 0 {
						limit = v
					}
					i++
				}
			}
		}
		resp, err := app.Search(query, status, limit)
		if err != nil {
			return err
		}
		return writeOK(resp)
	case "task":
		return runTask(app, args[1:])
	case "session":
		return runSession(app, args[1:])
	case "reservation":
		if len(args) > 1 && args[1] == "renew" {
			if len(args) < 3 {
				return solo.ErrInvalidArgument("missing task id")
			}
			resp, err := app.RenewReservation(args[2])
			if err != nil {
				return err
			}
			return writeOK(resp)
		}
		return solo.ErrInvalidArgument("unknown reservation command")
	case "handoff":
		return runHandoff(app, args[1:])
	case "worktree":
		return runWorktree(app, args[1:])
	case "audit":
		return runAudit(app, args[1:])
	case "recover":
		if len(args) >= 2 && args[1] == "--all" {
			resp, err := app.RecoverAll()
			if err != nil {
				return err
			}
			return writeOK(resp)
		}
		return solo.ErrInvalidArgument("expected --all")
	default:
		return solo.ErrInvalidArgument("unknown command")
	}
}

func runTask(app *solo.App, args []string) error {
	if len(args) == 0 {
		return solo.ErrInvalidArgument("missing task subcommand")
	}
	switch args[0] {
	case "create":
		title := ""
		typeVal := "task"
		priority := 3
		desc := ""
		ac := ""
		dod := ""
		parent := ""
		labels := []string{}
		affected := []string{}
		deps := []string{}
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--title":
				title = val(args, &i)
			case "--type":
				typeVal = val(args, &i)
			case "--priority":
				priority = parsePriority(val(args, &i), 3)
			case "--description":
				desc = val(args, &i)
			case "--acceptance-criteria":
				ac = val(args, &i)
			case "--definition-of-done":
				dod = val(args, &i)
			case "--parent":
				parent = val(args, &i)
			case "--labels":
				labels = splitCSV(val(args, &i))
			case "--affected-files":
				affected = splitCSV(val(args, &i))
			case "--deps":
				deps = splitCSV(val(args, &i))
			}
		}
		resp, err := app.CreateTask(solo.CreateTaskInput{
			Title: title, Type: typeVal, Priority: priority, Description: desc,
			AcceptanceCriteria: ac, DefinitionOfDone: dod, ParentTask: parent,
			Labels: labels, AffectedFiles: affected, Dependencies: deps,
		})
		if err != nil {
			return err
		}
		return writeOK(resp)
	case "list":
		status := ""
		label := ""
		available := false
		limit := 20
		offset := 0
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--status":
				status = val(args, &i)
			case "--label":
				label = val(args, &i)
			case "--available":
				available = true
			case "--limit":
				limit, _ = strconv.Atoi(val(args, &i))
			case "--offset":
				offset, _ = strconv.Atoi(val(args, &i))
			}
		}
		resp, err := app.ListTasks(status, label, available, limit, offset)
		if err != nil {
			return err
		}
		return writeOK(resp)
	case "show":
		if len(args) < 2 {
			return solo.ErrInvalidArgument("missing task id")
		}
		resp, err := app.ShowTask(args[1])
		if err != nil {
			return err
		}
		return writeOK(resp)
	case "update":
		if len(args) < 2 {
			return solo.ErrInvalidArgument("missing task id")
		}
		taskID := args[1]
		title := ""
		description := ""
		priority := ""
		parent := ""
		labels := []string{}
		affected := []string{}
		version := 0
		for i := 2; i < len(args); i++ {
			switch args[i] {
			case "--title":
				title = val(args, &i)
			case "--description":
				description = val(args, &i)
			case "--priority":
				priority = strconv.Itoa(parsePriority(val(args, &i), 0))
			case "--parent":
				parent = val(args, &i)
			case "--labels":
				labels = splitCSV(val(args, &i))
			case "--affected-files":
				affected = splitCSV(val(args, &i))
			case "--version":
				version, _ = strconv.Atoi(val(args, &i))
			}
		}
		resp, err := app.UpdateTask(taskID, title, description, priority, parent, labels, affected, version)
		if err != nil {
			return err
		}
		return writeOK(resp)
	case "ready":
		if len(args) < 2 {
			return solo.ErrInvalidArgument("missing task id")
		}
		version := 0
		for i := 2; i < len(args); i++ {
			if args[i] == "--version" {
				version, _ = strconv.Atoi(val(args, &i))
			}
		}
		resp, err := app.ForceReady(args[1], version)
		if err != nil {
			return err
		}
		return writeOK(resp)
	case "deps":
		if len(args) < 2 {
			return solo.ErrInvalidArgument("missing task id")
		}
		resp, err := app.TaskDeps(args[1])
		if err != nil {
			return err
		}
		return writeOK(resp)
	case "tree":
		if len(args) < 2 {
			return solo.ErrInvalidArgument("missing task id")
		}
		resp, err := app.TaskTree(args[1])
		if err != nil {
			return err
		}
		return writeOK(resp)
	case "recover":
		if len(args) < 2 {
			return solo.ErrInvalidArgument("missing task id")
		}
		version := 0
		for i := 2; i < len(args); i++ {
			if args[i] == "--version" {
				version, _ = strconv.Atoi(val(args, &i))
			}
		}
		resp, err := app.RecoverTask(args[1], version)
		if err != nil {
			return err
		}
		return writeOK(resp)
	default:
		if args[0] == "context" {
			if len(args) < 2 {
				return solo.ErrInvalidArgument("missing task id")
			}
			maxTokens := 0
			for i := 2; i < len(args); i++ {
				if args[i] == "--max-tokens" {
					maxTokens, _ = strconv.Atoi(val(args, &i))
				}
			}
			resp, err := app.TaskContext(args[1], maxTokens)
			if err != nil {
				return err
			}
			return writeOK(resp)
		}
		return solo.ErrInvalidArgument("unknown task subcommand")
	}
}

func runSession(app *solo.App, args []string) error {
	if len(args) == 0 {
		return solo.ErrInvalidArgument("missing session subcommand")
	}
	switch args[0] {
	case "start":
		if len(args) < 2 {
			return solo.ErrInvalidArgument("missing task id")
		}
		taskID := args[1]
		worker := ""
		ttl := 0
		pid := os.Getpid()
		for i := 2; i < len(args); i++ {
			switch args[i] {
			case "--worker":
				worker = val(args, &i)
			case "--ttl":
				ttl, _ = strconv.Atoi(val(args, &i))
			case "--pid":
				pid, _ = strconv.Atoi(val(args, &i))
			}
		}
		resp, err := app.StartSession(taskID, worker, ttl, pid)
		if err != nil {
			return err
		}
		return writeOK(resp)
	case "end":
		if len(args) < 2 {
			return solo.ErrInvalidArgument("missing task id")
		}
		taskID := args[1]
		result := ""
		notes := ""
		commits := []string{}
		files := []string{}
		status := ""
		for i := 2; i < len(args); i++ {
			switch args[i] {
			case "--result":
				result = val(args, &i)
			case "--notes":
				notes = val(args, &i)
			case "--commits":
				commits = splitCSV(val(args, &i))
			case "--files":
				files = splitCSV(val(args, &i))
			case "--status":
				status = val(args, &i)
			}
		}
		resp, err := app.EndSession(taskID, result, notes, commits, files, status)
		if err != nil {
			return err
		}
		return writeOK(resp)
	case "list":
		taskID := ""
		worker := ""
		active := false
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--task":
				taskID = val(args, &i)
			case "--worker":
				worker = val(args, &i)
			case "--active":
				active = true
			}
		}
		resp, err := app.ListSessions(taskID, worker, active)
		if err != nil {
			return err
		}
		return writeOK(resp)
	default:
		return solo.ErrInvalidArgument("unknown session subcommand")
	}
}

func runHandoff(app *solo.App, args []string) error {
	if len(args) == 0 {
		return solo.ErrInvalidArgument("missing handoff subcommand")
	}
	switch args[0] {
	case "create":
		if len(args) < 2 {
			return solo.ErrInvalidArgument("missing task id")
		}
		taskID := args[1]
		summary := ""
		remaining := ""
		to := ""
		files := []string{}
		for i := 2; i < len(args); i++ {
			switch args[i] {
			case "--summary":
				summary = val(args, &i)
			case "--remaining-work":
				remaining = val(args, &i)
			case "--to":
				to = val(args, &i)
			case "--files":
				files = splitCSV(val(args, &i))
			}
		}
		resp, err := app.CreateHandoff(taskID, summary, remaining, to, files)
		if err != nil {
			return err
		}
		return writeOK(resp)
	case "list":
		taskID := ""
		status := ""
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--task":
				taskID = val(args, &i)
			case "--status":
				status = val(args, &i)
			}
		}
		resp, err := app.ListHandoffs(taskID, status)
		if err != nil {
			return err
		}
		return writeOK(resp)
	case "show":
		if len(args) < 2 {
			return solo.ErrInvalidArgument("missing handoff id")
		}
		resp, err := app.ShowHandoff(args[1])
		if err != nil {
			return err
		}
		return writeOK(resp)
	default:
		return solo.ErrInvalidArgument("unknown handoff subcommand")
	}
}

func runWorktree(app *solo.App, args []string) error {
	if len(args) == 0 {
		return solo.ErrInvalidArgument("missing worktree subcommand")
	}
	switch args[0] {
	case "list":
		resp, err := app.ListWorktrees()
		if err != nil {
			return err
		}
		return writeOK(resp)
	case "inspect":
		if len(args) < 2 {
			return solo.ErrInvalidArgument("missing task id")
		}
		resp, err := app.InspectWorktree(args[1])
		if err != nil {
			return err
		}
		return writeOK(resp)
	case "cleanup":
		taskID := ""
		force := false
		for i := 1; i < len(args); i++ {
			if args[i] == "--force" {
				force = true
			} else if !strings.HasPrefix(args[i], "--") {
				taskID = args[i]
			}
		}
		resp, err := app.CleanupWorktrees(taskID, force)
		if err != nil {
			return err
		}
		return writeOK(resp)
	default:
		return solo.ErrInvalidArgument("unknown worktree subcommand")
	}
}

func hasFlag(args []string, target string) bool {
	for _, a := range args {
		if a == target {
			return true
		}
	}
	return false
}

func val(args []string, i *int) string {
	if *i+1 >= len(args) {
		return ""
	}
	*i = *i + 1
	return args[*i]
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return []string{}
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func runAudit(app *solo.App, args []string) error {
	if len(args) == 0 {
		return solo.ErrInvalidArgument("missing audit subcommand")
	}
	switch args[0] {
	case "list":
		taskID := ""
		limit := 50
		offset := 0
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--task":
				taskID = val(args, &i)
			case "--limit":
				limit, _ = strconv.Atoi(val(args, &i))
			case "--offset":
				offset, _ = strconv.Atoi(val(args, &i))
			}
		}
		resp, err := app.ListAudit(taskID, limit, offset)
		if err != nil {
			return err
		}
		return writeOK(resp)
	case "show":
		if len(args) < 2 {
			return solo.ErrInvalidArgument("missing event id")
		}
		id, _ := strconv.Atoi(args[1])
		resp, err := app.ShowAudit(id)
		if err != nil {
			return err
		}
		return writeOK(resp)
	default:
		return solo.ErrInvalidArgument("unknown audit subcommand")
	}
}

func parsePriority(raw string, fallback int) int {
	v, err := strconv.Atoi(raw)
	if err == nil && v >= 1 && v <= 5 {
		return v
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "low":
		return 2
	case "medium":
		return 3
	case "high":
		return 4
	case "critical":
		return 5
	default:
		return fallback
	}
}

func writeOK(v any) error {
	return writeJSON(map[string]any{"ok": true, "data": v})
}

func writeJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encode response: %w", err)
	}
	return nil
}
