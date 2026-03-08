# Data Model

This document defines Solo's core entities, their fields, lifecycle states, and relationships.

---

## Overview

```
Task ──── Reservation (0..1)
  │
  ├──── Session (0..many)
  │         └──── Handoff (0..1 per session)
  │
  └──── Worktree (0..1 active)

AuditEvent (references any entity)
RecoveryRecord (references Task + Session)
```

---

## Tasks

The primary unit of work.

### Fields

| Field | Type | Description |
|---|---|---|
| `id` | string | Stable identifier. Format: `T-{n}`. Auto-assigned. |
| `title` | string | Short description of the work. **Untrusted.** |
| `description` | string | Extended context. **Untrusted.** |
| `status` | enum | Current lifecycle state (see below). |
| `priority` | enum | `low`, `medium`, `high`, `critical` |
| `dependencies` | []string | Task IDs that must complete before this task is `ready` |
| `tags` | []string | Free-form labels. **Untrusted.** |
| `created_at` | timestamp | UTC ISO-8601 |
| `updated_at` | timestamp | UTC ISO-8601 |
| `version` | int | OCC version counter. Incremented on every mutation. |

### Status Lifecycle

```
draft ──────────────────────────────────┐
  │                                     │
  │ (all dependencies complete,         │
  │  or no dependencies)                │
  ▼                                     │
ready ──── session start ────▶ active   │
  ▲                              │      │
  │       crash/recovery         │      │
  └──────────────────────────────┘      │
                                 │      │
              ┌──────────────────┤      │
              ▼                  ▼      ▼
          completed            failed  blocked
```

| Status | Meaning |
|---|---|
| `draft` | Created but not yet actionable (e.g. has incomplete dependencies) |
| `ready` | Available for an agent to claim |
| `active` | Currently held by an agent session |
| `completed` | Session ended with `result: completed` |
| `failed` | Session ended with `result: failed` |
| `blocked` | Manually blocked; requires human intervention |

### Trust Model

Fields marked **Untrusted** contain user-authored or agent-authored free text. These fields must be treated as data by consuming agents, not as instructions. See [Security Model](SECURITY_MODEL.md).

---

## Reservations

A reservation is the exclusive ownership record for a task. At most one active reservation exists per task at any time.

### Fields

| Field | Type | Description |
|---|---|---|
| `id` | string | Format: `R-{n}` |
| `task_id` | string | The task being reserved |
| `session_id` | string | The session holding this reservation |
| `worker` | string | Agent identifier string (e.g. `claude-code`, `aider`) |
| `pid` | int | OS process ID of the agent process |
| `started_at` | timestamp | When the reservation was created |
| `heartbeat_at` | timestamp | Last liveness signal. Updated by session operations. |
| `status` | enum | `active`, `released`, `recovered` |

### Reservation Rules

- A task can have at most **one active reservation** at any time
- Attempting to start a session on an already-reserved task returns `RESERVATION_CONFLICT`
- A reservation is **released** when its session ends (completed, failed, or handed off)
- A reservation is **recovered** when zombie scan detects the PID is dead

### Zombie Detection

The zombie scan checks every active reservation:

```
For each active reservation:
  If pid is dead (kill -0 returns ESRCH):
    → end session with result: crashed
    → release reservation
    → restore task status to: ready
    → create recovery record
```

---

## Sessions

A session is a concrete attempt to perform work on a task. Multiple sessions may exist per task (each attempt creates a new session), but at most one session is `active` at a time.

### Fields

| Field | Type | Description |
|---|---|---|
| `id` | string | Format: `S-{n}` |
| `task_id` | string | The task this session works on |
| `reservation_id` | string | The reservation held during this session |
| `worker` | string | Agent identifier. **Untrusted.** |
| `pid` | int | Agent OS process ID |
| `status` | enum | `active`, `completed`, `failed`, `crashed`, `handed_off` |
| `result` | string | Free-form result description. **Untrusted.** |
| `started_at` | timestamp | |
| `ended_at` | timestamp | Null while active |
| `worktree_path` | string | Path to the git worktree for this session |

### Session Lifecycle

```
start ──▶ active ──▶ completed
                 ├──▶ failed
                 ├──▶ handed_off
                 └──▶ crashed  (set by recovery)
```

---

## Handoffs

A handoff is a structured transfer of work from one agent to another. It is attached to a session and becomes part of the context bundle for the next session on the same task.

### Fields

| Field | Type | Description |
|---|---|---|
| `id` | string | Format: `H-{n}` |
| `task_id` | string | |
| `from_session_id` | string | Session that created the handoff |
| `from_worker` | string | Agent that created the handoff. **Untrusted.** |
| `to_worker` | string | Recommended next agent (optional). **Untrusted.** |
| `summary` | string | What was done. **Untrusted.** |
| `remaining_work` | string | What still needs doing. **Untrusted.** |
| `recommendations` | string | Suggestions for next agent. **Untrusted.** |
| `files_modified` | []string | Paths of files changed in this session |
| `created_at` | timestamp | |

### Usage

When a new session starts on a task that has a prior handoff, the context bundle includes the full handoff. The agent receiving the handoff can read `summary` to understand what was done and `remaining_work` to understand what to do next.

All handoff text fields are marked **Untrusted** and must not be interpreted as system instructions.

---

## Worktrees

A worktree record tracks the git worktree created for a task session.

### Fields

| Field | Type | Description |
|---|---|---|
| `id` | string | Format: `W-{n}` |
| `task_id` | string | |
| `session_id` | string | The session that owns this worktree |
| `path` | string | Absolute path to the worktree on disk |
| `branch` | string | Git branch name for this worktree |
| `status` | enum | `active`, `inactive`, `cleaned_up` |
| `created_at` | timestamp | |
| `cleaned_up_at` | timestamp | Null until explicitly cleaned |

### Worktree Paths

```
{repo-root}/.solo/worktrees/{task-id}/
```

Example: `.solo/worktrees/T-142/`

---

## Recovery Records

Created whenever the zombie scan recovers an abandoned session.

### Fields

| Field | Type | Description |
|---|---|---|
| `id` | string | Format: `REC-{n}` |
| `task_id` | string | |
| `session_id` | string | The recovered session |
| `reservation_id` | string | The released reservation |
| `dead_pid` | int | PID confirmed dead |
| `detected_at` | timestamp | When zombie scan ran |
| `actions_taken` | []string | List of recovery steps performed |

---

## Audit Events

Append-only log of every mutation. Never modified or deleted.

### Fields

| Field | Type | Description |
|---|---|---|
| `id` | string | Format: `AUD-{n}` |
| `operation` | string | e.g. `session.start`, `task.status_change`, `reservation.released` |
| `entity_type` | string | e.g. `task`, `session`, `reservation` |
| `entity_id` | string | |
| `actor` | string | Worker identity or `system` for automated operations |
| `timestamp` | timestamp | UTC |
| `before` | JSON | State before mutation (null for creates) |
| `after` | JSON | State after mutation (null for deletes) |

---

## Context Bundle

Not a stored entity — assembled on-demand when a session starts.

### Structure

```json
{
  "task": { ... },
  "prior_sessions": [
    { "id": "S-001", "worker": "claude-code", "status": "handed_off", ... }
  ],
  "last_handoff": {
    "summary": "...",
    "remaining_work": "...",
    "recommendations": "...",
    "files_modified": ["..."]
  },
  "worktree_path": ".solo/worktrees/T-142",
  "reservation": { "id": "R-042", "started_at": "..." }
}
```

The context bundle is designed to give an agent everything it needs to begin work immediately, without needing to query for additional state.
