# Solo

> Local orchestration layer for coordinating multiple coding agents on a single machine.

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go)](https://golang.org/)
[![Status: Pre-release](https://img.shields.io/badge/Status-Pre--release-orange)]()

---

## The Problem

Modern development increasingly relies on multiple AI coding agents running in sequence or parallel:

- **Claude Code** — implementation
- **Aider** — refactoring
- **Codex CLI** — scaffolding
- **Cursor agents** — inline edits
- **Windsurf** — codebase-wide changes

Without coordination, these agents:

- duplicate work already in progress
- lose context between sessions
- break handoffs — the next agent doesn't know what the previous one did
- overwrite each other's changes in shared branches

There is currently no durable coordination layer between agents. Solo fills that gap.

---

## Core Idea

Solo is a **local coordination ledger**.

It tracks:

| Entity | Purpose |
|---|---|
| Tasks | Units of work with status and metadata |
| Reservations | Active ownership locks — one agent per task |
| Sessions | Concrete work attempts with history |
| Handoffs | Structured transfers between agents |
| Worktrees | Isolated git workspaces per task |
| Context Bundles | Packaged state for agent consumption |

All state is stored locally in **SQLite** (WAL mode).

Agents interact with Solo via a **deterministic CLI + JSON interface**.

---

## Example Workflow

### 1 — Find available work

```bash
solo task list --available --json
```

```json
{
  "tasks": [
    { "id": "T-142", "title": "Fix retry logic in HTTP client", "status": "ready", "priority": "high" }
  ]
}
```

### 2 — Start a session

```bash
solo session start T-142 --worker claude-code --json
```

This atomically:

- reserves the task (locks it against other agents)
- creates an isolated git worktree at `.solo/worktrees/T-142`
- returns a full context bundle

```json
{
  "session_id": "S-089",
  "worktree_path": ".solo/worktrees/T-142",
  "context": {
    "task": { "id": "T-142", "title": "Fix retry logic in HTTP client" },
    "prior_sessions": 1,
    "last_handoff": { "summary": "Implemented base retry. Tests still failing." }
  }
}
```

### 3 — Work inside the worktree

Agents edit files, commit changes, run tests — all inside the isolated worktree.

### 4 — Complete or hand off

**Complete:**

```bash
solo session end T-142 --result completed --json
```

**Hand off to another agent:**

```bash
solo handoff create T-142 \
  --summary "Retry logic implemented, exponential backoff added" \
  --remaining-work "Fix failing integration test on line 142" \
  --to aider \
  --json
```

---

## Why Solo Exists

AI coding agents are powerful but **stateless**.

Between sessions, they forget:

- what other agents already did
- what task they were working on
- what remains to be done
- what decisions were made and why

Solo provides **persistent coordination memory** that outlives any individual agent session.

---

## Features

- **Local task tracker** — create, prioritize, and manage work items
- **Reservation locking** — atomic ownership guarantees via Optimistic Concurrency Control
- **Session history** — full audit trail of every work attempt
- **Structured handoffs** — machine-readable transfer packages with summary + remaining work
- **Git worktree isolation** — each task gets its own working directory
- **Context bundle generation** — agents receive structured state, not raw DB dumps
- **Automatic crash recovery** — dead PIDs and abandoned sessions are reclaimed on next invocation
- **SQLite WAL concurrency** — safe for concurrent agent processes
- **JSON-first CLI** — every command outputs structured JSON for automation
- **Read-only web dashboard** — optional live UI + `/metrics` endpoint for Grafana/Prometheus

---

## Philosophy

Solo is intentionally minimal.

| Principle | Meaning |
|---|---|
| Local only | No network, no cloud, no accounts |
| No daemon | No background scheduler. Core lifecycle stays CLI-driven |
| Optional dashboard server | `solo dashboard` is read-only and opt-in for observability |
| Deterministic | Same inputs always produce same outputs |
| Ledger, not orchestrator | Solo tracks what happened. It never runs agents |

> **Agents call Solo. Solo never calls agents.**

---

## Installation

```bash
git clone https://github.com/v1truv1us/Solo.git
cd Solo
make build
```

Then initialize a Git repository with Solo and install the repo-local skill bundle to provide agent guidance in `.solo/skills`:

```bash
./solo init --install-skill --json
```

---

## Quick Start

```bash
# Initialize a repository
solo init

# Create a task
solo task create --title "Fix retry logic" --priority high

# List available work
solo task list --available

# Start a session as an agent
solo session start T-1 --worker claude-code

# Get full task context
solo task context T-1

# ... do work in the worktree ...

# Complete the session
solo session end T-1 --result completed

# Or hand off to another agent
solo handoff create T-1 --summary "Done" --remaining-work "Tests" --to aider

# Optional: start read-only dashboard + metrics endpoint
solo dashboard --addr :8081
# UI: http://localhost:8081/
# Prometheus: http://localhost:8081/metrics
```

---

## Documentation

| Document | Description |
|---|---|
| [PRD](PRD%20(1).md) | Product requirements and user stories |
| [Architecture](ARCHITECTURE.md) | System design and component overview |
| [Design Principles](DESIGN_PRINCIPLES%20(1).md) | Philosophy and constraints |
| [Data Model](DATA_MODEL%20(1).md) | Core entities and schema |
| [CLI Reference](CLI_REFERENCE%20(1).md) | Full command reference |
| [Agent Integration](AGENT_INTEGRATION.md) | How agents should use Solo |
| [Failure & Recovery](FAILURE_AND_RECOVERY%20(1).md) | Crash handling and recovery |
| [Security Model](SECURITY_MODEL.md) | Trust boundaries and threat model |
| [Testing Strategy](TESTING_STRATEGY.md) | Test categories and coverage |
| [Roadmap](ROADMAP.md) | Planned and future work |
| [Grafana Dashboard](grafana/README.md) | Setup Solo metrics in Grafana/Prometheus |

---

## Status

Solo is in active pre-release development. The CLI surface and data model are stabilizing toward v1.

---

## License

MIT
