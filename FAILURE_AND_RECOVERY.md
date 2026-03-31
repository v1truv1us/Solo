# Failure and Recovery

Solo is designed to handle failure gracefully. This document describes every failure mode, how Solo detects it, and what recovery action is taken.

---

## Design Philosophy

Solo's recovery model is based on three principles:

1. **Never lose work.** A crashed session should restore the task to a recoverable state, not discard it.
2. **Self-healing.** Recovery runs automatically on every CLI invocation. No manual intervention required in normal cases.
3. **Audit everything.** Every recovery action is recorded in the audit log and as an explicit recovery record.

---

## Failure Mode 1: Agent Process Crash

**What happens:** An agent process dies unexpectedly (SIGKILL, segfault, OOM, power loss) while holding a reservation on a task.

**How Solo detects it:**

Every active session records the agent's PID in `sessions.agent_pid`. The zombie scanner, which runs at the start of every CLI invocation, checks each active session's PID with:

```go
err := syscall.Kill(pid, 0)
// ESRCH means process does not exist
```

If the process is dead, the session is marked as crashed.

**Recovery actions:**

1. End the session with `status: crashed` and `ended_at` set
2. Mark reservation as inactive with release_reason = 'recovered'
3. Restore task status to `ready`
4. Create a recovery record with: `task_id`, `session_id`, `dead_pid`, `detected_at`, `actions_taken`
5. Write audit events for each state change

**Result:** The task appears in `--available` on the next `task list` call. A new agent can start a fresh session, and the context bundle will include prior session history including the crashed session.

---

## Failure Mode 2: Abandoned Session (Agent Hung)

**What happens:** An agent process is alive but has stopped making progress. It holds a reservation but isn't doing work.

**How Solo detects it:**

PID-based detection alone cannot distinguish "actively working" from "hung." Solo does not have a heartbeat mechanism.

**Current behavior:** Solo does not automatically recover hung-but-alive processes in v1. If a developer identifies a hung agent, they can:

1. Kill the agent process
2. The next CLI invocation's zombie scan will detect the dead PID and recover

**Future consideration:** A configurable reservation TTL could force recovery of sessions that haven't updated `heartbeat_at` within a threshold. Not implemented in v1.

---

## Failure Mode 3: Partial Session Start

**What happens:** `session start` partially succeeds — the reservation is created but the worktree creation fails.

**How Solo handles it:**

Session start is wrapped in a transaction. If worktree creation fails after the DB transaction commits, Solo:

1. Rolls back the reservation
2. Returns `WORKTREE_ERROR` to the agent
3. Task status is restored

If the process crashes between the DB commit and the worktree creation, the zombie scanner will detect the orphaned reservation on the next invocation and recover it.

---

## Failure Mode 4: Database Corruption

**What happens:** The SQLite file is corrupted (disk error, abrupt power loss during write).

**How Solo handles it:**

SQLite WAL mode provides significant protection against corruption:
- WAL writes are atomic
- Incomplete writes are rolled back on next open
- Checksums catch most corruption

If corruption is detected at startup, Solo returns `DB_ERROR` and refuses to operate.

**Recovery:** Use SQLite's built-in `PRAGMA integrity_check` and `.recover` command. The audit log, being append-only, is the most resilient table and can be used to reconstruct task state.

**Backup recommendation:** For important projects, periodically back up `.solo/solo.db`. It is a single file.

---

## Failure Mode 5: Git Worktree Errors

**What happens:** A worktree operation fails — `git worktree add` returns an error, or the worktree directory is unexpectedly missing.

**How Solo handles it:**

- If `git worktree add` fails during `session start`, the session is not created. `WORKTREE_ERROR` is returned.
- If a worktree is missing when a session is active (e.g. someone manually deleted it), Solo reports the inconsistency via `solo worktree inspect` but does not automatically recreate it.

**Manual recovery:**
```bash
# Inspect the worktree state
solo worktree inspect T-142

# End the session manually if needed
solo session end T-142 --result failed --summary "Worktree missing"

# Re-start with a fresh session
solo session start T-142 --worker claude-code
```

---

## Failure Mode 6: OCC Conflict

**What happens:** Two agents try to modify the same entity (e.g. both try to start a session on the same task) simultaneously.

**How Solo handles it:**

Optimistic Concurrency Control catches this at the database level. The second write fails because its `WHERE version = {expected}` clause doesn't match (the first write already incremented the version).

Solo returns `OCC_CONFLICT` to the losing agent.

**Agent action:** Retry once after a short delay (100ms). If the retry fails, the resource is genuinely contended — fall back to picking a different task.

---

## Failure Mode 7: Disk Full

**What happens:** Disk fills up during worktree creation or database write.

**How Solo handles it:**

- If the DB write fails with a disk error, the transaction is rolled back. Solo returns `DB_ERROR`.
- If worktree creation fails mid-way (partial files), Solo marks the worktree as `error` and returns `WORKTREE_ERROR`.

**Recovery:** Free disk space and retry. Solo does not automatically clean up partial worktrees — use `solo worktree cleanup` after freeing space.

---

## The Zombie Scanner

The zombie scanner is the core of Solo's self-healing capability.

**When it runs:** At the start of every CLI invocation, before the requested operation executes.

**What it does:**

```
For each session where ended_at IS NULL:
  pid = session.agent_pid
  if pid > 0 AND is_dead(pid):
    begin transaction
      session.status    = 'crashed'
      session.ended_at  = now()
      reservation.active = 0
      reservation.release_reason = 'recovered'
      task.status       = 'ready'
      task.version      += 1
      create recovery_record
      create audit_events
    commit
```

Note: If `session.agent_pid` is NULL (e.g., started from CLI without --pid flag), the session is not treated as a zombie since there's no valid PID to check.

**Performance:** The scan is O(n) where n is the number of active sessions. In practice this is almost always a small number (< 10). The overhead is negligible.

**Idempotency:** Running the scanner multiple times is safe. A session that already has `ended_at` set is not re-processed.

**CLI vs Agent Sessions:** When a session is started from the CLI without the `--pid` flag, `agent_pid` is stored as NULL. This prevents false crash detection since CLI processes exit immediately after the command completes. Agents should always pass their PID via `--pid` to enable proper zombie detection.

---

## Manual Recovery Commands

**Recover all abandoned sessions:**
```bash
solo recover --all --json
```

**Recover a specific task:**
```bash
solo task recover T-142 --json
```

**Inspect recovery history:**
```bash
solo task show T-142 --json
# Check the sessions array for crashed sessions and recovery records
```

---

## Recovery Record Schema

```json
{
  "id": "REC-003",
  "task_id": "T-15",
  "session_id": "S-042",
  "reservation_id": "R-031",
  "dead_pid": 88221,
  "detected_at": "2024-01-15T14:22:00Z",
  "actions_taken": [
    "session.status → crashed",
    "reservation.status → recovered",
    "task.status → ready"
  ]
}
```

---

## What Solo Does Not Recover

- **Uncommitted git changes** in a worktree from a crashed session. These remain in the worktree directory and are not lost, but Solo does not commit or stash them automatically.
- **In-progress file writes** at the OS level. If an agent crashed mid-write, the file may be truncated or corrupt. This is outside Solo's control.
- **Task progress made in the agent's working memory.** If an agent had analyzed requirements and formed a plan but hadn't committed anything, that mental state is lost. This is why handoffs with good summaries are valuable even for in-progress work.
