# CLI Reference

All commands support `--json` for structured output. JSON is the preferred output format for agent use.

All commands exit `0` on success, non-zero on failure. JSON error responses include a machine-readable `code` field.

---

## Global Flags

| Flag | Description |
|---|---|
| `--json` | Output structured JSON instead of human-readable text |
| `--db <path>` | Override database path (default: `.solo/solo.db`) |
| `--verbose` | Include additional diagnostic output |

---

## System Commands

### `solo init`

Initialize Solo in the current repository.

Creates `.solo/` directory and initializes the SQLite database with current schema.

```bash
solo init
solo init --json
```

**Output:**
```json
{ "ok": true, "data": { "db_path": ".solo/solo.db", "schema_version": 1 } }
```

**Errors:**
- `ALREADY_INITIALIZED` — `.solo/` already exists

---

### `solo health`

Check the health of the Solo installation.

```bash
solo health
solo health --json
```

**Output:**
```json
{
  "ok": true,
  "data": {
    "db_path": ".solo/solo.db",
    "schema_version": 3,
    "active_sessions": 2,
    "pending_recovery": 0,
    "worktrees": { "active": 2, "cleanup_pending": 1 }
  }
}
```

---

### `solo search`

Full-text search across tasks.

```bash
solo search "retry logic"
solo search "retry logic" --json
solo search "retry logic" --status ready
```

**Flags:**
| Flag | Description |
|---|---|
| `--status <status>` | Filter results by task status |
| `--limit <n>` | Maximum results to return (default: 20) |

---

### `solo dashboard`

Start an optional read-only web dashboard with JSON and metrics endpoints.

```bash
solo dashboard --addr :8081
```

Served endpoints:

- `/` — HTML dashboard
- `/api/dashboard` — deterministic JSON snapshot (`{ "ok": true, "data": ... }`)
- `/metrics` — Prometheus text format metrics

**Flags:**
| Flag | Description |
|---|---|
| `--addr <host:port>` | Listen address (default: `:8081`) |

**Core metrics exported:**

- `solo_tasks_total`
- `solo_tasks_by_status{status="..."}`
- `solo_active_sessions`
- `solo_active_reservations`
- `solo_pending_handoffs`
- `solo_worktrees_active`
- `solo_worktrees_cleanup_pending`
- `solo_db_size_bytes`
- `solo_zombie_sessions`

---

## Task Commands

### `solo task create`

Create a new task.

```bash
solo task create --title "Fix retry logic in HTTP client"
solo task create \
  --title "Fix retry logic" \
  --description "Exponential backoff is not applied on 429 responses" \
  --priority high \
  --deps T-140,T-141 \
  --json
```

**Flags:**
| Flag | Description |
|---|---|
| `--title <string>` | Task title (required) |
| `--description <string>` | Extended description |
| `--priority <level>` | `low`, `medium` (default), `high`, `critical` |
| `--deps <ids>` | Comma-separated task IDs that must complete first |
| `--tags <tags>` | Comma-separated tags |

**Output:**
```json
{
  "ok": true,
  "data": {
    "id": "T-142",
    "title": "Fix retry logic in HTTP client",
    "status": "ready",
    "priority": "high"
  }
}
```

---

### `solo task list`

List tasks with optional filters.

```bash
solo task list
solo task list --available --json
solo task list --status active
solo task list --worker claude-code
```

**Flags:**
| Flag | Description |
|---|---|
| `--available` | Only tasks with status `ready` and no active reservation |
| `--status <status>` | Filter by status |
| `--priority <level>` | Filter by priority |
| `--worker <name>` | Filter by currently assigned worker |
| `--limit <n>` | Maximum results (default: 50) |
| `--offset <n>` | Pagination offset |

**Output:**
```json
{
  "ok": true,
  "data": {
    "tasks": [
      { "id": "T-142", "title": "Fix retry logic", "status": "ready", "priority": "high" }
    ],
    "total": 1
  }
}
```

---

### `solo task show`

Show full detail for a single task.

```bash
solo task show T-142
solo task show T-142 --json
```

**Output:**
```json
{
  "ok": true,
  "data": {
    "id": "T-142",
    "title": "Fix retry logic in HTTP client",
    "description": "...",
    "status": "active",
    "priority": "high",
    "dependencies": ["T-140"],
    "sessions": [...],
    "active_reservation": { ... }
  }
}
```

---

### `solo task update`

Update task fields.

```bash
solo task update T-142 --priority critical
solo task update T-142 --title "New title" --json
```

**Flags:**
| Flag | Description |
|---|---|
| `--title <string>` | |
| `--description <string>` | |
| `--priority <level>` | |
| `--tags <tags>` | Replaces existing tags |

---

### `solo task ready`

Manually mark a task as ready (overrides dependency check).

```bash
solo task ready T-142
```

Use when dependencies are satisfied externally or you want to force a blocked task into the ready queue.

---

### `solo task deps`

Show dependency status for a task.

```bash
solo task deps T-142
solo task deps T-142 --json
```

**Output:**
```json
{
  "ok": true,
  "data": {
    "task_id": "T-142",
    "dependencies": [
      { "id": "T-140", "title": "...", "status": "completed" },
      { "id": "T-141", "title": "...", "status": "ready" }
    ],
    "blocking": ["T-141"]
  }
}
```

---

### `solo task tree`

Show the full dependency tree for a task.

```bash
solo task tree T-142
```

---

### `solo task context`

Get the full context bundle for a task. This is what an agent receives when starting a session.

```bash
solo task context T-142
solo task context T-142 --json
```

---

### `solo task recover`

Manually trigger recovery for a specific task.

```bash
solo task recover T-142
solo task recover T-142 --json
```

Useful if automatic recovery during normal invocations hasn't run yet and you need immediate recovery.

---

## Session Commands

### `solo session start`

Start a work session on a task. Atomically:

1. Checks task is available (status: `ready`, no active reservation)
2. Creates reservation
3. Creates git worktree
4. Creates session record
5. Assembles and returns context bundle

```bash
solo session start T-142 --worker claude-code --json
```

**Flags:**
| Flag | Description |
|---|---|
| `--worker <name>` | Agent identifier string (required) |
| `--pid <n>` | Agent process ID for zombie detection (default: 0 = NULL in DB). Agents should pass their actual PID to enable crash detection. Range: 0-4194304 |

**Output:**
```json
{
  "ok": true,
  "data": {
    "session_id": "S-089",
    "task_id": "T-142",
    "worktree_path": ".solo/worktrees/T-142",
    "reservation_id": "R-042",
    "context": { ... }
  }
}
```

**Errors:**
- `RESERVATION_CONFLICT` — task is already reserved by another session
- `TASK_NOT_READY` — task status is not `ready`
- `WORKTREE_ERROR` — failed to create git worktree

---

### `solo session end`

End the current session for a task.

```bash
solo session end T-142 --result completed --json
solo session end T-142 --result failed --summary "Build fails on arm64" --json
```

**Flags:**
| Flag | Description |
|---|---|
| `--result <result>` | `completed` or `failed` (required) |
| `--summary <string>` | Optional summary of what was done |

**Output:**
```json
{
  "ok": true,
  "data": {
    "session_id": "S-089",
    "task_id": "T-142",
    "result": "completed",
    "task_status": "completed"
  }
}
```

---

### `solo session list`

List sessions with optional filters.

```bash
solo session list
solo session list --task T-142
solo session list --worker claude-code --status active
```

**Flags:**
| Flag | Description |
|---|---|
| `--task <id>` | Filter by task ID |
| `--worker <name>` | Filter by worker |
| `--status <status>` | Filter by session status |

---

## Handoff Commands

### `solo handoff create`

Create a handoff to transfer work to another agent.

```bash
solo handoff create T-142 \
  --summary "Implemented exponential backoff. All unit tests pass." \
  --remaining-work "Integration test on line 142 still failing — needs mock HTTP server" \
  --to aider \
  --files internal/http/retry.go,internal/http/retry_test.go \
  --json
```

**Flags:**
| Flag | Description |
|---|---|
| `--summary <string>` | What was done in this session (required) |
| `--remaining-work <string>` | What still needs to be done (required) |
| `--to <worker>` | Recommended next agent (optional) |
| `--recommendations <string>` | Additional notes for next agent |
| `--files <paths>` | Comma-separated list of modified files |

**Side effects:**
- Ends the current session with status `handed_off`
- Releases the reservation
- Sets task status back to `ready`

---

### `solo handoff list`

List handoffs, optionally filtered by task.

```bash
solo handoff list
solo handoff list --task T-142
```

---

### `solo handoff show`

Show full detail for a handoff.

```bash
solo handoff show H-007 --json
```

---

## Worktree Commands

### `solo worktree list`

List all worktrees.

```bash
solo worktree list
solo worktree list --status active
```

---

### `solo worktree inspect`

Show full detail for a worktree.

```bash
solo worktree inspect W-012
solo worktree inspect T-142   # Look up by task ID
```

---

### `solo worktree cleanup`

Delete inactive worktrees from disk.

```bash
solo worktree cleanup          # Clean all inactive worktrees
solo worktree cleanup T-142    # Clean worktree for specific task
solo worktree cleanup --all    # Clean all, including completed tasks
```

**Safety:** Only worktrees with `active` or `cleanup_pending` status are eligible. The command removes the git worktree from disk and deletes the database record.

---

## Recovery Commands

### `solo recover --all`

Run zombie recovery manually across all active sessions.

```bash
solo recover --all
solo recover --all --json
```

Note: This also runs automatically at the start of every CLI invocation.

**Output:**
```json
{
  "ok": true,
  "data": {
    "scanned": 4,
    "recovered": 1,
    "records": [
      { "task_id": "T-139", "session_id": "S-085", "dead_pid": 49231 }
    ]
  }
}
```

---

## Error Codes

| Code | Meaning |
|---|---|
| `RESERVATION_CONFLICT` | Task is already reserved by another agent |
| `TASK_NOT_READY` | Task status does not permit the requested operation |
| `TASK_NOT_FOUND` | No task with the given ID exists |
| `SESSION_NOT_FOUND` | No session with the given ID exists |
| `WORKTREE_ERROR` | Git worktree operation failed |
| `WORKTREE_EXISTS` | A worktree already exists for this task |
| `OCC_CONFLICT` | Optimistic concurrency conflict; retry the operation |
| `ALREADY_INITIALIZED` | `solo init` run on already-initialized repo |
| `DEPENDENCY_UNMET` | Task has unmet dependencies |
| `INVALID_ARGUMENT` | A required flag was missing or invalid (e.g., --pid out of range) |
| `DB_ERROR` | Internal database error |
