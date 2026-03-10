package solo

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var safeAgentName = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

func installSoloSkill(root, scope, agent string) (string, error) {
	scope = strings.ToLower(strings.TrimSpace(scope))
	if scope == "" {
		scope = "environment"
	}

	var dir string
	switch scope {
	case "environment", "env":
		dir = filepath.Join(root, ".solo", "skills", "solo")
	case "agent":
		agent = strings.TrimSpace(agent)
		if agent == "" {
			return "", ErrInvalidArgument("--agent is required when --skill-scope agent")
		}
		if !safeAgentName.MatchString(agent) {
			return "", ErrInvalidArgument("invalid --agent value")
		}
		dir = filepath.Join(root, ".solo", "skills", "agents", agent, "solo")
	default:
		return "", ErrInvalidArgument("--skill-scope must be environment or agent")
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	skillPath := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(soloSkillTemplate()), 0o644); err != nil {
		return "", err
	}
	return skillPath, nil
}

func soloSkillTemplate() string {
	return `---
name: solo
description: Coordinate multi-agent coding work using the Solo CLI ledger. Use when managing task lifecycle, reservations, sessions, handoffs, audit events, or context bundles for OpenCode, OpenClaw, Claude Code, Codex, and other coding agents.
---

# Solo Skill

Use Solo as a ledger, not an orchestrator.

## Core flow

1. Initialize/check state
	solo init --json
	solo task list --available --json

2. Start session
	solo session start <task-id> --worker <stable-agent-id> --json

3. Update progress
	solo task update <task-id> --status active --version <n> --json

4. End or handoff
	solo session end <task-id> --summary "..." --json
	# or
	solo handoff create <task-id> --to <next-agent> --summary "..." --remaining-work "..." --json

## Useful commands

	solo task tree <task-id> --json
	solo audit list --task <task-id> --json
	solo audit show <event-id> --json
	solo health --json
`
}

func skillInstallSummary(scope, path string) map[string]any {
	return map[string]any{
		"installed": true,
		"scope":     scope,
		"path":      path,
		"hint":      fmt.Sprintf("Load this skill from %s in your agent environment", path),
	}
}
