# Product Requirements Document

**Product:** Solo  
**Version:** 1.0  
**Status:** Pre-release  
**Category:** Local AI development tooling

---

## Problem Statement

Modern software development increasingly uses multiple AI coding agents in combination. A typical workflow might involve a planning agent that decomposes requirements, a coding agent that implements features, a testing agent that writes and runs tests, and a debugging agent that investigates failures.

These agents are individually capable, but they operate in isolation. They have no shared memory, no awareness of what the others have done, and no mechanism to safely transfer work between them.

The result is predictable and expensive:

- **Duplication** — two agents pick up the same task simultaneously
- **Context loss** — a new agent session starts without knowledge of prior decisions
- **Broken handoffs** — the next agent doesn't know what the previous one left unfinished
- **State corruption** — agents editing the same files in the same branch overwrite each other

There is no existing tool that acts as a durable coordination layer between local agents. Solo fills this gap.

---

## Goal

Build a local task orchestration system that provides:

1. **Exclusive ownership** — only one agent works on a task at a time
2. **Context preservation** — all relevant state is available to each new session
3. **Structured handoffs** — work transfers include summary, remaining work, and recommendations
4. **Crash resilience** — abandoned or crashed sessions are automatically recovered

The system must be local-first, require no daemon or server, and expose a deterministic JSON CLI that any agent can drive programmatically.

---

## Target Users

### Primary: Solo developers using multiple coding agents

Developers who use tools like Claude Code, Aider, Codex CLI, Cursor, or Windsurf as part of their workflow and want to coordinate them without manually tracking state.

### Secondary: AI-assisted development teams (future)

Teams that want a shared coordination layer across agents running on different machines. (Out of scope for v1.)

### Tertiary: Tooling builders

Developers building agent orchestration systems who want a lightweight local backend.

---

## User Stories

### As a developer, I want to track tasks so I have a single source of truth for what needs to be done.

**Acceptance criteria:**
- Tasks can be created with title, description, priority, and dependencies
- Tasks have a status lifecycle: `draft → ready → active → completed | failed | blocked`
- Tasks can be listed, filtered, and searched from the CLI

---

### As an agent, I want to reserve a task so I know I'm the only one working on it.

**Acceptance criteria:**
- Starting a session atomically reserves the task
- No second agent can start a session on a reserved task
- Reservation includes agent identity and start timestamp
- Reservation is released when session ends or agent crashes

---

### As a developer, I want to recover work after an agent crash so nothing is lost.

**Acceptance criteria:**
- Every CLI invocation runs a zombie scan
- Sessions attached to dead PIDs are automatically ended
- Reservations are released when their session ends
- Task status is restored to `ready` after crash recovery
- A recovery record is created for audit purposes

---

### As an agent, I want structured handoff instructions when work is transferred to me.

**Acceptance criteria:**
- Handoffs include: summary of completed work, remaining work description, optional next agent recommendation, and file list
- Handoff is returned as part of the context bundle when a new session starts on a handed-off task
- Handoff data is marked `untrusted` to prevent prompt injection

---

### As an agent, I want an isolated git worktree so my changes don't conflict with other agents.

**Acceptance criteria:**
- Each task session creates a worktree at `.solo/worktrees/{task-id}`
- Worktrees are cleaned up when sessions complete
- Worktree path is included in the session start response

---

### As a developer, I want a full audit history so I can understand what happened on any task.

**Acceptance criteria:**
- Every state mutation creates an audit event
- Events include: operation, actor, timestamp, before/after state
- Audit log is queryable from the CLI

---

## Non-Goals (v1)

The following are explicitly out of scope for v1:

- **Multi-machine coordination** — Solo is local only
- **Remote backup or sync** — no network operations
- **Agent scheduling** — Solo tracks tasks, it does not assign or run agents
- **Visual dashboard** — CLI only
- **Authentication or access control** — single-user local tool
- **Plugin system** — no extension mechanism in v1

---

## Success Criteria

Solo v1 succeeds if:

| Criterion | Measure |
|---|---|
| No task duplication | Zero cases of two agents holding active sessions on the same task simultaneously |
| Context preserved across sessions | Prior session summaries and handoffs are always included in context bundles |
| Crash recovery works | All abandoned sessions are reclaimed within one subsequent CLI invocation |
| Concurrent safety | No state corruption under concurrent agent load |
| JSON interface stable | All commands produce valid, schema-conformant JSON output |

---

## Constraints

- **Go standard library preferred** — minimize dependencies
- **No network I/O** — all operations are local
- **No daemon** — no background process; each CLI invocation is self-contained
- **SQLite only** — no external database
- **Single binary** — one executable, no runtime dependencies

---

## Open Questions

| Question | Status |
|---|---|
| Should worktrees be cleaned up immediately on session end, or lazily? | Decided: lazily, with explicit cleanup command |
| Should handoffs expire? | Decided: no expiry in v1 |
| Should Solo support task dependencies in v1? | Decided: yes, DAG-based dependency tracking |
