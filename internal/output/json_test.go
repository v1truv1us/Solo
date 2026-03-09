package output

import (
	"testing"
)

func TestSoloErrorImplementsError(t *testing.T) {
	err := NewError(ErrTaskNotFound, "task not found", false, "check ID")
	if err.Error() == "" {
		t.Error("expected non-empty error string")
	}
	if err.Code != ErrTaskNotFound {
		t.Errorf("expected code %s, got %s", ErrTaskNotFound, err.Code)
	}
	if err.Retryable {
		t.Error("expected not retryable")
	}
}

func TestNewError(t *testing.T) {
	err := NewError(ErrVersionConflict, "version mismatch", true, "retry")
	if err.Code != ErrVersionConflict {
		t.Errorf("expected %s, got %s", ErrVersionConflict, err.Code)
	}
	if !err.Retryable {
		t.Error("expected retryable")
	}
	if err.RetryHint != "retry" {
		t.Errorf("expected retry hint 'retry', got %q", err.RetryHint)
	}
}

func TestAllErrorCodes(t *testing.T) {
	codes := []string{
		ErrNotARepo, ErrTaskNotFound, ErrTaskNotReady, ErrTaskLocked,
		ErrNoActiveSession, ErrHandoffLocked, ErrVersionConflict,
		ErrInvalidTransition, ErrCircularDependency, ErrRepoEmpty,
		ErrWorktreeDirty, ErrWorktreeExists, ErrWorktreeLimitExceeded,
		ErrGitIndexLocked, ErrGitError, ErrBaseRefNotFound,
		ErrAlreadyInitialized, ErrSQLiteBusy, ErrInvalidArgument, ErrDBError,
	}
	for _, code := range codes {
		if code == "" {
			t.Errorf("empty error code found")
		}
	}
}
