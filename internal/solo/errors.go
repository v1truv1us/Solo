package solo

import "fmt"

type Error struct {
	Code             string   `json:"code"`
	Message          string   `json:"message"`
	Retryable        bool     `json:"retryable"`
	RetryHint        string   `json:"retry_hint,omitempty"`
	CurrentStatus    string   `json:"current_status,omitempty"`
	RequestedStatus  string   `json:"requested_status,omitempty"`
	ValidTransitions []string `json:"valid_transitions,omitempty"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func errWith(code, message string, retryable bool, hint string) *Error {
	return &Error{Code: code, Message: message, Retryable: retryable, RetryHint: hint}
}

func NewInternalError(err error) *Error {
	return errWith("INTERNAL_ERROR", err.Error(), false, "")
}

func ErrInvalidArgument(msg string) *Error {
	return errWith("INVALID_ARGUMENT", msg, false, "")
}

func errNotRepo() *Error {
	return errWith("NOT_A_REPO", "No .git found walking up from current directory.", false, "Run inside a git repo")
}

func errTaskNotFound(taskID string) *Error {
	return errWith("TASK_NOT_FOUND", "Task not found: "+taskID, false, "Check ID with solo task list")
}

func errTaskNotReady(status string) *Error {
	return errWith("TASK_NOT_READY", "Task is not ready; current status is '"+status+"'.", false, "Check status with solo task show")
}

func errTaskLocked(taskID string) *Error {
	return errWith("TASK_LOCKED", "Task has an active reservation: "+taskID, false, "Wait for agent to finish, or use solo task recover")
}

func errNoActiveSession(taskID string) *Error {
	return errWith("NO_ACTIVE_SESSION", "No active session for task: "+taskID, false, "Check solo session list --task "+taskID)
}

func errVersionConflict() *Error {
	return errWith("VERSION_CONFLICT", "Optimistic concurrency conflict; expected version no longer matches.", true, "Re-read current version and retry")
}

func errInvalidTransition(from, to string, valid []string) *Error {
	msg := fmt.Sprintf("Cannot transition from '%s' to '%s'. Valid transitions from '%s': %v.", from, to, from, valid)
	err := errWith("INVALID_TRANSITION", msg, false, "Check status transition matrix")
	err.CurrentStatus = from
	err.RequestedStatus = to
	err.ValidTransitions = valid
	return err
}

func errCircularDependency() *Error {
	return errWith("CIRCULAR_DEPENDENCY", "Dependency would create a cycle.", false, "Review dependency graph")
}

func errRepoEmpty() *Error {
	return errWith("REPO_EMPTY", "Repository has no commits.", false, "Make an initial commit before using worktrees")
}

func errWorktreeDirty(path string) *Error {
	return errWith("WORKTREE_DIRTY", "Worktree has uncommitted changes: "+path, false, "Commit/stash, or use --force")
}

func errWorktreeExists(path string) *Error {
	return errWith("WORKTREE_EXISTS", "Worktree path already exists: "+path, false, "Run solo worktree cleanup")
}

func errWorktreeLimitExceeded(max int) *Error {
	return errWith("WORKTREE_LIMIT_EXCEEDED", fmt.Sprintf("Active worktrees exceed max_worktrees (%d)", max), false, "Clean up with solo worktree cleanup")
}

func errGitIndexLocked() *Error {
	return errWith("GIT_INDEX_LOCKED", "git index.lock exists.", true, "Auto-retried 3x; retry shortly")
}

func errGitError(msg string) *Error {
	return errWith("GIT_ERROR", msg, false, "Check git output in message")
}

func errBaseRefNotFound(ref string) *Error {
	return errWith("BASE_REF_NOT_FOUND", "Configured base_ref not found: "+ref, false, "Run git fetch or update config")
}

func errAlreadyInitialized() *Error {
	return errWith("ALREADY_INITIALIZED", "Solo is already initialized in this repository.", false, "")
}

func errSQLiteBusy() *Error {
	return errWith("SQLITE_BUSY", "Database is busy.", true, "Retry after brief delay")
}

func errHandoffLocked() *Error {
	return errWith("HANDOFF_LOCKED", "Pending handoff is locked to a specific worker.", false, "Pick a different task")
}

func errWorktreeError(msg string) *Error {
	return errWith("WORKTREE_ERROR", msg, false, "Inspect git output and retry")
}

func errBranchExists(branch string) *Error {
	return errWith("BRANCH_EXISTS", "Branch already exists: "+branch, false, "Use --branch override")
}
