# Solo GitHub Issue Fixes — Atomic Implementation Plan

---

## Fix 1: Delete cleaned worktree rows instead of marking them
**Issue:** #8 — cleaned worktree rows block re-starting the same task with WORKTREE_EXISTS
**Scope:** 4 files, 5 atomic changes

### Root Cause
`CleanupWorktrees` (`worktrees.go:103`) sets `status='cleaned'` on the row but keeps it. `StartSession` (`sessions.go:84`) does `INSERT INTO worktrees` with `path` as PK — unique constraint fails because the cleaned row still occupies that key, even though the directory is gone from disk.

### Change 1.1 — worktrees.go:103 — DELETE instead of UPDATE
**File:** `internal/solo/worktrees.go`
**Line:** 103
**Current:**
```go
_, err := conn.ExecContext(ctx, `UPDATE worktrees SET status='cleaned', cleaned_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE path=?`, path)
```
**Change to:**
```go
_, err := conn.ExecContext(ctx, `DELETE FROM worktrees WHERE path=?`, path)
```

### Change 1.2 — worktrees.go:65 — Remove `status!='cleaned'` filter from cleanup query
**File:** `internal/solo/worktrees.go`
**Line:** 65
**Current:**
```go
query := `SELECT path, task_id, branch_name FROM worktrees WHERE status!='cleaned'`
```
**Change to:**
```go
query := `SELECT path, task_id, branch_name FROM worktrees`
```
**Reason:** After Fix 1.1, cleaned rows are deleted — no rows will ever have `status='cleaned'`. This filter is now dead code.

### Change 1.3 — worktrees.go:37 — Remove `status!='cleaned'` filter from InspectWorktree
**File:** `internal/solo/worktrees.go`
**Line:** 37
**Current:**
```go
if err := db.QueryRow(`SELECT path, tID, branch, status, base_ref, COALESCE(disk_usage_bytes, 0) FROM worktrees WHERE task_id=? AND status!='cleaned' ORDER BY created_at DESC LIMIT 1`, taskID).
```
**Change to:**
```go
if err := db.QueryRow(`SELECT path, tID, branch, status, base_ref, COALESCE(disk_usage_bytes, 0) FROM worktrees WHERE task_id=? ORDER BY created_at DESC LIMIT 1`, taskID).
```

### Change 1.4 — schema.go:103 — Remove 'cleaned' from worktree status CHECK constraint
**File:** `internal/solo/schema.go`
**Line:** 103
**Current:**
```sql
status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'cleanup_pending', 'cleaned')),
```
**Change to:**
```sql
status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'cleanup_pending')),
```
**Reason:** `cleaned` is no longer a valid status since rows are deleted instead.

### Change 1.5 — schema.go:106 — Remove `cleaned_at` column
**File:** `internal/solo/schema.go`
**Line:** 106
**Current:**
```
cleaned_at TEXT
```
**Change to:** Remove the line entirely.
**Reason:** No row will ever get a `cleaned_at` timestamp since we delete instead of clean.

### Verification
- Run `go test ./internal/solo/...`
- Manual: `solo session start T-1 --worker w1 && solo session end T-1 --result completed && solo worktree cleanup T-1 && solo session start T-1 --worker w1` — second start must succeed

---

## Fix 2: Default agent_pid to NULL when --pid is not supplied
**Issue:** #7 — session start defaults to transient CLI PID and gets auto-recovered as crash_detected
**Scope:** 3 files, 4 atomic changes

### Root Cause
`cmd/solo/main.go:335` sets `pid := os.Getpid()`. This is the PID of the short-lived `solo` CLI process. When the process exits, `lazyZombieScan` (`recovery.go:56`) calls `isProcessDead(pid)` which returns true, triggering crash recovery on the next CLI invocation.

### Change 2.1 — cmd/solo/main.go:335 — Default pid to 0 instead of os.Getpid()
**File:** `cmd/solo/main.go`
**Line:** 335
**Current:**
```go
pid := os.Getpid()
```
**Change to:**
```go
pid := 0
```
**Reason:** 0 signals "no PID provided". The `--pid` flag at line 342-343 still allows explicit PID.

### Change 2.2 — internal/solo/sessions.go:76 — Use sql.NullInt64 for pid
**File:** `internal/solo/sessions.go`
**Line:** 76
**Current:**
```go
if _, err := conn.ExecContext(ctx, `INSERT INTO sessions (id, task_id, reservation_id, worker_id, agent_pid) VALUES (?, ?, ?, ?, ?)`, sessionID, taskID, resID, worker, pid); err != nil {
```
**Change to:**
```go
var pidVal any
if pid > 0 {
    pidVal = pid
}
if _, err := conn.ExecContext(ctx, `INSERT INTO sessions (id, task_id, reservation_id, worker_id, agent_pid) VALUES (?, ?, ?, ?, ?)`, sessionID, taskID, resID, worker, pidVal); err != nil {
```
**Reason:** When `pid` is 0, `pidVal` is nil which SQLite stores as NULL. When explicitly provided, the actual PID is stored.

### Change 2.3 — internal/solo/recovery.go:39-42 — Already correct
**File:** `internal/solo/recovery.go`
**Line:** 42
**Current query:**
```sql
WHERE s.ended_at IS NULL AND s.agent_pid IS NOT NULL AND r.active=1
```
**No change needed.** The zombie scan already filters `agent_pid IS NOT NULL`. When Fix 2.2 stores NULL for missing pid, these sessions are excluded from zombie scanning automatically.

### Change 2.4 — internal/solo/system.go:47 — Health zombie count already correct
**File:** `internal/solo/system.go`
**Line:** 47 (zombie count query)
**Current:**
```sql
SELECT agent_pid FROM sessions WHERE ended_at IS NULL AND agent_pid IS NOT NULL
```
**No change needed.** Same reasoning — `agent_pid IS NOT NULL` already excludes sessions without PID.

### Verification
- Run `go test ./internal/solo/...`
- Manual: `solo session start T-1 --worker w1` (no --pid) → `solo session list --active` → session should remain active, not be crash-recovered

---

## Fix 3: Response envelope parity — already implemented
**Issue:** #6 Gap 1 — CLI responses should follow `{ok,data}/{ok,error}` envelope

### Analysis
On inspection, the current code already implements this correctly:

- **Success path:** `cmd/solo/main.go:604-606` — `writeOK(v)` wraps in `{"ok": true, "data": v}`. Every command handler calls `writeOK(resp)`.
- **Error path:** `cmd/solo/main.go:17-23` — errors are wrapped in `{"ok": false, "error": se}` where `se` is a `*solo.Error` struct with `code`, `message`, `retryable`, etc.
- **Error struct:** `internal/solo/errors.go:5-13` — `Error` struct has `Code`, `Message`, `Retryable`, `RetryHint`, `CurrentStatus`, `RequestedStatus`, `ValidTransitions`.

**Conclusion:** This gap is already closed. No changes needed.

---

## Fix 4: Task lifecycle + priority semantics — already aligned
**Issue:** #6 Gap 2 — task lifecycle and priority diverge from spec docs

### Analysis
On inspection, the implementation already matches the documented spec:

- **Schema** (`schema.go:16`): `status IN ('draft', 'ready', 'active', 'completed', 'failed', 'blocked', 'cancelled')` — matches spec
- **Transitions** (`tasks.go:9-17`): `draft→ready→active→completed|failed|blocked` — matches spec
- **Priority** (`semantics.go:41-69`): CLI accepts `low|medium|high|critical` string input via `parsePriorityValue`, maps to numeric 2-5 internally
- **Output** (`semantics.go:71-82`): `priorityLabel()` converts numeric back to `low|medium|high|critical` string
- **Migration** (`schema.go:166-170`): Schema version 2 migrates legacy statuses (`open→draft`, `in_progress→active`, `done→completed`)
- **Compatibility** (`status_storage.go`): `taskStatusForWrite` handles legacy DB schemas for backward compat
- **Normalization** (`semantics.go:5-19`): `normalizeTaskStatus` accepts both new and legacy values
- **Display** (`tasks.go:81`): Both canonical and legacy status are returned: `"status": "active", "status_legacy": "in_progress"`

**Conclusion:** This gap is already closed. The migration in schema version 2 and the compatibility layer in `semantics.go`/`status_storage.go` handle the alignment. No changes needed.

---

## Fix 5: Missing command surface — task tree + audit commands
**Issue:** #6 Gap 3 — documented `task tree` and audit commands not implemented

### Analysis
On inspection, these commands are **already implemented**:

- **`task tree`**: `internal/solo/tasks.go:375-409` — `TaskTree()` method with recursive CTE. `cmd/solo/main.go:278-286` — CLI handler for `task tree <task-id>`.
- **`audit list`**: `internal/solo/audit.go:8-39` — `ListAudit()` with task filter, limit, offset. `cmd/solo/main.go:537-574` — CLI handler.
- **`audit show`**: `internal/solo/audit.go:41-60` — `ShowAudit()` by event ID. CLI handler in same block.

**Conclusion:** This gap is already closed. No changes needed.

---

## Summary: Only 2 Fixes Required

After thorough codebase analysis, issues #6 and its 3 sub-gaps are **already resolved** in the current codebase. Only issues #8 and #7 require changes.

| Fix | Issue | Files Changed | Changes |
|-----|-------|---------------|---------|
| Fix 1 | #8 (WORKTREE_EXISTS) | `worktrees.go`, `schema.go` | 5 edits |
| Fix 2 | #7 (false crash_detected) | `cmd/solo/main.go`, `sessions.go` | 2 edits |

### Execution Order
1. **Fix 2 first** (agent_pid) — 2 edits, zero risk of cross-contamination
2. **Fix 1 second** (worktree delete) — 5 edits, touches schema

### Verification Command
```bash
go build ./cmd/solo && go test ./internal/solo/...
```