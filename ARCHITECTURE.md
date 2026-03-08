# Architecture Overview

**Product:** Solo  
**Version:** 1.0

---

## Summary

Solo is a three-layer system:

```
┌─────────────────────────────────┐
│           solo CLI              │  Stateless binary. Entry point.
├─────────────────────────────────┤
│         SQLite Database         │  WAL mode. ACID. Local file.
├─────────────────────────────────┤
│         Git Worktrees           │  Per-task isolated workspaces.
└─────────────────────────────────┘
```

There is no background process, no server, and no network layer.

---

## Layer 1: CLI

The CLI is a stateless binary. Every invocation is a complete, atomic operation.

**Invocation lifecycle:**

```
1. Parse flags and arguments
2. Open SQLite connection
3. Run zombie recovery scan (always)
4. Execute the requested operation (transactionally)
5. Write JSON to stdout
6. Exit
```

The zombie scan in step 3 ensures that crashed agents are cleaned up before any operation runs. This means the database is always self-healing — no cron job or daemon required.

**Output contract:**

Every command outputs either:

```json
{ "ok": true, "data": { ... } }
```

or:

```json
{ "ok": false, "error": { "code": "RESERVATION_CONFLICT", "message": "..." } }
```

Exit code mirrors success/failure. Agents must check both.

---

## Layer 2: SQLite Database

**Location:** `.solo/solo.db` (relative to repository root)

**Mode:** WAL (Write-Ahead Log)

WAL mode provides:
- Readers never block writers
- Writers never block readers
- Safe for concurrent agent processes
- Full ACID guarantees

**Concurrency model:**

Solo uses **Optimistic Concurrency Control (OCC)** on critical entities (tasks, reservations). Each row carries a `version` integer. Updates include a `WHERE version = expected` clause and fail if the row was modified concurrently.

This means:
- No pessimistic locking
- Concurrent operations are attempted and retried if needed
- Version conflicts surface as structured errors, not silent overwrites

**Schema principles:**
- All timestamps are stored as UTC ISO-8601 strings
- Foreign keys are enforced
- Soft deletes only — no row is ever physically deleted
- Audit events are append-only

---

## Layer 3: Git Worktrees

Each active task session creates a git worktree at:

```
.solo/worktrees/{task-id}/
```

This provides **hard filesystem isolation** between agents:

- Agent working on `T-142` has its own working directory
- Agent working on `T-199` has a completely separate tree
- Neither agent can accidentally touch the other's files
- Merge conflicts between parallel sessions are impossible during active work

**Worktree lifecycle:**

```
session start  →  worktree created at .solo/worktrees/{task-id}
session end    →  worktree marked inactive
worktree cleanup  →  worktree deleted from disk (explicit command)
```

Worktrees are not deleted automatically on session end. This preserves work in case of unexpected issues. Cleanup is an explicit operation.

---

## Data Flow

```
Agent Process
    │
    │  CLI invocation (e.g. solo session start T-142 --worker claude-code --json)
    ▼
solo binary
    │
    ├── 1. Zombie recovery scan
    │       └── query sessions with dead PIDs
    │           └── end session + release reservation + restore task status
    │
    ├── 2. Begin SQLite transaction
    │
    ├── 3. Execute operation
    │       ├── Validate inputs
    │       ├── Check preconditions (e.g. task is available)
    │       ├── Apply state change (with OCC version check)
    │       ├── Create worktree (if session start)
    │       ├── Write audit event
    │       └── Commit transaction
    │
    └── 4. Write JSON to stdout → Agent reads response
```

---

## Package Structure

```
solo/
├── cmd/
│   └── solo/
│       └── main.go              # Entry point, flag parsing
├── internal/
│   ├── db/
│   │   ├── schema.go            # Schema definition and migrations
│   │   ├── tasks.go             # Task CRUD operations
│   │   ├── sessions.go          # Session operations
│   │   ├── reservations.go      # Reservation locking
│   │   ├── handoffs.go          # Handoff operations
│   │   ├── worktrees.go         # Worktree record operations
│   │   ├── audit.go             # Audit event writes
│   │   └── recovery.go          # Zombie scan and recovery
│   ├── git/
│   │   └── worktree.go          # Git worktree create/delete/inspect
│   ├── context/
│   │   └── bundle.go            # Context bundle assembly
│   ├── cli/
│   │   ├── task.go              # Task command handlers
│   │   ├── session.go           # Session command handlers
│   │   ├── handoff.go           # Handoff command handlers
│   │   ├── worktree.go          # Worktree command handlers
│   │   ├── recover.go           # Recovery command handlers
│   │   └── system.go            # init, health, search handlers
│   └── output/
│       └── json.go              # JSON output formatting
├── docs/
├── README.md
└── Makefile
```

---

## Key Design Decisions

### No daemon

Every CLI invocation is fully self-contained. This eliminates an entire class of failure modes (daemon crashes, stale sockets, startup race conditions) and makes Solo trivially deployable — just a binary.

### OCC over pessimistic locking

Pessimistic locking (e.g. `SELECT FOR UPDATE`) blocks. With multiple agents running concurrently, blocking creates unpredictable latency and deadlock risk. OCC retries on conflict, which is fast and safe for the low-contention workloads Solo handles.

### Worktrees over branches

Using git worktrees instead of just branches gives agents true filesystem isolation. An agent working in `.solo/worktrees/T-142` literally cannot see or touch files that another agent is editing in `.solo/worktrees/T-199`.

### Audit log as append-only truth

The audit log is never modified after creation. This means that even if other tables are mutated, the complete history of what happened to every entity is always recoverable from the audit log alone.

### Untrusted field tagging

Any field that can contain user-authored or agent-authored text (task titles, descriptions, handoff summaries) is explicitly tagged `trust_level: untrusted` in the data model. This prevents prompt injection — agents consuming Solo's output must treat these fields as data, not instructions.
