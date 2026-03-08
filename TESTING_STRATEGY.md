# Testing Strategy

Solo's testing strategy is organized around four categories of risk: correctness of individual operations, safety under concurrency, end-to-end workflow integrity, and resilience under failure conditions.

---

## Principles

**Test behavior, not implementation.** Tests should verify what Solo does, not how it does it internally. This allows refactoring without breaking tests.

**Use the real database.** Unit tests use an in-memory SQLite database (`:memory:`), not mocks. Testing against the actual database engine catches SQL errors, constraint violations, and transaction behavior that mocks cannot.

**Test failure paths explicitly.** Every error code in the CLI reference has at least one test that verifies it is returned in the correct condition.

**Concurrency tests are not optional.** Race conditions in a coordination tool are critical failures. The concurrency test suite runs on every CI build.

---

## Test Categories

### 1. Unit Tests

Test individual operations in isolation against an in-memory SQLite database.

**Coverage targets:**

| Component | Target |
|---|---|
| Task CRUD | 100% of status transitions |
| Reservation creation and release | All success and conflict paths |
| Session lifecycle | All terminal states |
| Handoff creation | Valid and invalid inputs |
| Context bundle assembly | With/without prior sessions, with/without handoffs |
| OCC version conflict | Every entity that uses OCC |
| Zombie detection | PID alive, PID dead, PID reused |
| Dependency resolution | Chain, diamond, cycle detection |

**Example: Reservation conflict test**

```go
func TestReservationConflict(t *testing.T) {
    db := testDB(t)
    task := createReadyTask(t, db)

    // First reservation succeeds
    _, err := db.CreateReservation(task.ID, "agent-a", os.Getpid())
    require.NoError(t, err)

    // Second reservation on same task fails
    _, err = db.CreateReservation(task.ID, "agent-b", os.Getpid())
    require.ErrorIs(t, err, ErrReservationConflict)
}
```

**Example: Status transition test**

```go
func TestTaskStatusTransitions(t *testing.T) {
    tests := []struct {
        from    TaskStatus
        to      TaskStatus
        allowed bool
    }{
        {StatusReady, StatusActive, true},
        {StatusActive, StatusCompleted, true},
        {StatusActive, StatusFailed, true},
        {StatusCompleted, StatusActive, false},
        {StatusReady, StatusCompleted, false},
    }
    // ...
}
```

---

### 2. Concurrency Tests

Verify that concurrent agent operations do not corrupt state.

These tests spawn multiple goroutines (simulating multiple agents) and run operations simultaneously.

**Scenarios:**

**Concurrent session starts on the same task:**
- N goroutines all call `session start` on the same `ready` task simultaneously
- Exactly one must succeed
- All others must receive `RESERVATION_CONFLICT`
- Final DB state: exactly one `active` session, one `active` reservation

```go
func TestConcurrentSessionStart(t *testing.T) {
    db := testDB(t)
    task := createReadyTask(t, db)

    const N = 10
    results := make(chan error, N)

    for i := 0; i < N; i++ {
        go func(worker string) {
            _, err := StartSession(db, task.ID, worker, os.Getpid())
            results <- err
        }(fmt.Sprintf("agent-%d", i))
    }

    var successes, conflicts int
    for i := 0; i < N; i++ {
        err := <-results
        if err == nil {
            successes++
        } else if errors.Is(err, ErrReservationConflict) {
            conflicts++
        } else {
            t.Fatalf("unexpected error: %v", err)
        }
    }

    require.Equal(t, 1, successes)
    require.Equal(t, N-1, conflicts)
}
```

**Concurrent session starts on different tasks:**
- N goroutines each start a session on a distinct `ready` task
- All N must succeed without errors
- No OCC conflicts expected

**Concurrent task list reads during session start:**
- Readers calling `task list` while writers are creating sessions
- Readers must never see inconsistent state (task `active` with no reservation)

**Zombie recovery under concurrent load:**
- Active sessions while zombie scanner runs
- Scanner must not recover live sessions
- Scanner must recover all dead-PID sessions

---

### 3. Integration Tests

End-to-end tests that exercise complete workflows through the CLI binary.

These tests invoke the actual `solo` binary via `os/exec` and parse JSON output.

**Workflow: Single agent completes a task**

```
init repo
task create
session start → verify context bundle
  (create files in worktree)
session end --result completed
task show → verify status: completed
worktree list → verify worktree inactive
```

**Workflow: Handoff between two agents**

```
init repo
task create
agent-a: session start
agent-a: handoff create --summary "..." --remaining-work "..."
  → verify task status: ready (reservation released)
agent-b: session start T-1
  → verify context bundle contains handoff from agent-a
agent-b: session end --result completed
  → verify task status: completed
```

**Workflow: Crash recovery**

```
init repo
task create
session start (record PID)
simulate crash (kill process / mark PID dead in test)
run any solo command
  → verify zombie scan ran
  → verify task status: ready
  → verify recovery record created
session start (new agent)
  → verify prior_sessions includes crashed session
```

**Workflow: Dependency chain**

```
task create T-1
task create T-2 --deps T-1
  → verify T-2 status: draft
task list --available
  → verify only T-1 appears
session start T-1
session end T-1 --result completed
  → verify T-2 status: ready
task list --available
  → verify T-2 now appears
```

---

### 4. Failure Mode Tests

Verify that Solo handles adverse conditions correctly.

**Disk full simulation:**
- Fill the test volume to capacity
- Attempt session start
- Verify `DB_ERROR` or `WORKTREE_ERROR` returned
- Verify no partial state left in DB

**Database corruption:**
- Corrupt the SQLite file header
- Verify Solo returns `DB_ERROR` and exits non-zero
- Verify no panic

**Git not available:**
- Remove git from PATH
- Verify `solo init` returns useful error
- Verify `session start` returns `WORKTREE_ERROR`

**PID reuse edge case:**
- Start session with PID X
- Kill the process at PID X
- A new unrelated process starts with PID X
- Verify zombie scanner does not recover the session (PID is alive, even though wrong process)
- This is a known limitation documented in FAILURE_AND_RECOVERY.md

**Interrupted OCC operation:**
- Force an OCC conflict at the application level
- Verify `OCC_CONFLICT` returned
- Verify DB state is consistent

---

## Test Helpers

The test suite provides a standard set of helper functions:

```go
// Create an in-memory test database with schema applied
func testDB(t *testing.T) *DB

// Create a task in 'ready' status
func createReadyTask(t *testing.T, db *DB) *Task

// Create a task in 'draft' status
func createDraftTask(t *testing.T, db *DB) *Task

// Start a session and return session + context bundle
func startSession(t *testing.T, db *DB, taskID, worker string) (*Session, *ContextBundle)

// Simulate a dead PID (marks reservation PID as a known-dead value)
func simulateDeadPID(t *testing.T, reservation *Reservation)

// Run the full zombie scan and return recovery records
func runZombieScan(t *testing.T, db *DB) []*RecoveryRecord

// Assert task has expected status
func assertTaskStatus(t *testing.T, db *DB, taskID string, expected TaskStatus)
```

---

## CI Requirements

Every pull request must pass:

- All unit tests
- All concurrency tests (run with `-race` flag)
- All integration tests
- `go vet`
- `staticcheck`
- `govulncheck`

Coverage threshold: **80% line coverage** on `internal/db` and `internal/cli` packages.

---

## Running Tests Locally

```bash
# All tests
make test

# Unit tests only
go test ./internal/...

# With race detector
go test -race ./internal/...

# Integration tests (requires git in PATH)
go test ./cmd/...

# Specific test
go test -run TestConcurrentSessionStart ./internal/db/...

# Coverage report
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```
