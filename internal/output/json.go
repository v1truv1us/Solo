package output

import (
	"encoding/json"
	"fmt"
	"os"
)

// SoloError represents a structured error per spec §10.
type SoloError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	RetryHint string `json:"retry_hint,omitempty"`
	// Extra fields for transition errors
	CurrentStatus   string   `json:"current_status,omitempty"`
	RequestedStatus string   `json:"requested_status,omitempty"`
	ValidTransitions []string `json:"valid_transitions,omitempty"`
}

func (e *SoloError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Error codes per spec §10.
const (
	ErrNotARepo              = "NOT_A_REPO"
	ErrTaskNotFound          = "TASK_NOT_FOUND"
	ErrTaskNotReady          = "TASK_NOT_READY"
	ErrTaskLocked            = "TASK_LOCKED"
	ErrNoActiveSession       = "NO_ACTIVE_SESSION"
	ErrHandoffLocked         = "HANDOFF_LOCKED"
	ErrVersionConflict       = "VERSION_CONFLICT"
	ErrInvalidTransition     = "INVALID_TRANSITION"
	ErrCircularDependency    = "CIRCULAR_DEPENDENCY"
	ErrRepoEmpty             = "REPO_EMPTY"
	ErrWorktreeDirty         = "WORKTREE_DIRTY"
	ErrWorktreeExists        = "WORKTREE_EXISTS"
	ErrWorktreeLimitExceeded = "WORKTREE_LIMIT_EXCEEDED"
	ErrGitIndexLocked        = "GIT_INDEX_LOCKED"
	ErrGitError              = "GIT_ERROR"
	ErrBaseRefNotFound       = "BASE_REF_NOT_FOUND"
	ErrAlreadyInitialized    = "ALREADY_INITIALIZED"
	ErrSQLiteBusy            = "SQLITE_BUSY"
	ErrInvalidArgument       = "INVALID_ARGUMENT"
	ErrDBError               = "DB_ERROR"
	ErrWorktreeError         = "WORKTREE_ERROR"
	ErrBranchExists          = "BRANCH_EXISTS"
)

// NewError creates a SoloError.
func NewError(code, message string, retryable bool, hint string) *SoloError {
	return &SoloError{
		Code:      code,
		Message:   message,
		Retryable: retryable,
		RetryHint: hint,
	}
}

// ErrorResponse wraps an error for JSON output.
type ErrorResponse struct {
	Error *SoloError `json:"error"`
}

// PrintJSON writes a success response to stdout.
func PrintJSON(data interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(data)
}

// PrintError writes an error response to stdout and returns exit code 1.
func PrintError(err *SoloError) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(ErrorResponse{Error: err})
}

// PrintText writes plain text to stdout.
func PrintText(format string, args ...interface{}) {
	fmt.Fprintf(os.Stdout, format, args...)
}

// PrintErrorText writes an error message to stderr.
func PrintErrorText(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format, args...)
}
