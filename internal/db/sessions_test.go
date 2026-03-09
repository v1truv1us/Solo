package db_test

import (
	"database/sql"
	"testing"

	"github.com/v1truv1us/solo/internal/db"
	"github.com/v1truv1us/solo/internal/output"
)

func createReadyTask(t *testing.T, database *sql.DB) *db.Task {
	t.Helper()
	task, err := db.CreateTask(database, db.CreateTaskParams{Title: "Session test task"})
	if err != nil {
		t.Fatalf("creating task: %v", err)
	}
	readyStatus := "ready"
	task, err = db.UpdateTask(database, db.UpdateTaskParams{
		TaskID:  task.ID,
		Version: task.Version,
		Status:  &readyStatus,
	})
	if err != nil {
		t.Fatalf("setting task ready: %v", err)
	}
	return task
}

func TestStartSession(t *testing.T) {
	database := testDB(t)
	task := createReadyTask(t, database)

	result, err := db.StartSession(database, task.ID, "worker1", 1, 3600)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if result.SessionID == "" {
		t.Error("expected session ID")
	}
	if result.ReservationID == "" {
		t.Error("expected reservation ID")
	}
	if result.TaskID != task.ID {
		t.Errorf("expected task ID %s, got %s", task.ID, result.TaskID)
	}
	if result.ExpiresAt == "" {
		t.Error("expected expires_at")
	}

	// Verify task status changed to in_progress
	got, _ := db.GetTask(database, task.ID)
	if got.Status != "in_progress" {
		t.Errorf("expected in_progress, got %q", got.Status)
	}
}

func TestStartSessionNotReady(t *testing.T) {
	database := testDB(t)
	db.CreateTask(database, db.CreateTaskParams{Title: "Not ready"})

	_, err := db.StartSession(database, "T-1", "worker1", 1, 3600)
	if err == nil {
		t.Error("expected TASK_NOT_READY")
	}
	soloErr, ok := err.(*output.SoloError)
	if !ok {
		t.Fatalf("expected SoloError, got %T", err)
	}
	if soloErr.Code != output.ErrTaskNotReady {
		t.Errorf("expected TASK_NOT_READY, got %s", soloErr.Code)
	}
}

func TestStartSessionDoubleReservation(t *testing.T) {
	database := testDB(t)
	task := createReadyTask(t, database)

	_, err := db.StartSession(database, task.ID, "worker1", 1, 3600)
	if err != nil {
		t.Fatalf("first session: %v", err)
	}

	// Second attempt should fail with TASK_NOT_READY (task is now in_progress)
	_, err = db.StartSession(database, task.ID, "worker2", 1, 3600)
	if err == nil {
		t.Error("expected error on double reservation")
	}
}

func TestStartSessionEmptyWorker(t *testing.T) {
	database := testDB(t)

	_, err := db.StartSession(database, "T-1", "", 1, 3600)
	if err == nil {
		t.Error("expected error for empty worker")
	}
}

func TestEndSession(t *testing.T) {
	database := testDB(t)
	task := createReadyTask(t, database)

	session, _ := db.StartSession(database, task.ID, "worker1", 1, 3600)

	result, err := db.EndSession(database, db.EndSessionParams{
		TaskID: task.ID,
		Result: "completed",
		Notes:  "All done",
	})
	if err != nil {
		t.Fatalf("end session: %v", err)
	}
	if result.SessionID != session.SessionID {
		t.Errorf("session ID mismatch")
	}
	if result.Result != "completed" {
		t.Errorf("expected completed, got %q", result.Result)
	}
	if result.TaskStatus != "in_review" {
		t.Errorf("expected in_review after completed, got %q", result.TaskStatus)
	}
	if result.EndedAt == "" {
		t.Error("expected ended_at")
	}
}

func TestEndSessionStatusDone(t *testing.T) {
	database := testDB(t)
	task := createReadyTask(t, database)
	db.StartSession(database, task.ID, "worker1", 1, 3600)

	result, err := db.EndSession(database, db.EndSessionParams{
		TaskID:         task.ID,
		Result:         "completed",
		StatusOverride: "done",
	})
	if err != nil {
		t.Fatalf("end session: %v", err)
	}
	if result.TaskStatus != "done" {
		t.Errorf("expected done, got %q", result.TaskStatus)
	}
}

func TestEndSessionFailed(t *testing.T) {
	database := testDB(t)
	task := createReadyTask(t, database)
	db.StartSession(database, task.ID, "worker1", 1, 3600)

	result, err := db.EndSession(database, db.EndSessionParams{
		TaskID: task.ID,
		Result: "failed",
	})
	if err != nil {
		t.Fatalf("end session: %v", err)
	}
	if result.TaskStatus != "in_progress" {
		t.Errorf("expected in_progress after failed, got %q", result.TaskStatus)
	}
}

func TestEndSessionInterrupted(t *testing.T) {
	database := testDB(t)
	task := createReadyTask(t, database)
	db.StartSession(database, task.ID, "worker1", 1, 3600)

	result, err := db.EndSession(database, db.EndSessionParams{
		TaskID: task.ID,
		Result: "interrupted",
	})
	if err != nil {
		t.Fatalf("end session: %v", err)
	}
	if result.TaskStatus != "ready" {
		t.Errorf("expected ready after interrupted, got %q", result.TaskStatus)
	}
}

func TestEndSessionInvalidResult(t *testing.T) {
	database := testDB(t)

	_, err := db.EndSession(database, db.EndSessionParams{
		TaskID: "T-1",
		Result: "invalid",
	})
	if err == nil {
		t.Error("expected error for invalid result")
	}
}

func TestEndSessionNoActiveSession(t *testing.T) {
	database := testDB(t)
	db.CreateTask(database, db.CreateTaskParams{Title: "No session"})

	_, err := db.EndSession(database, db.EndSessionParams{
		TaskID: "T-1",
		Result: "completed",
	})
	if err == nil {
		t.Error("expected NO_ACTIVE_SESSION")
	}
	soloErr, ok := err.(*output.SoloError)
	if !ok {
		t.Fatalf("expected SoloError, got %T", err)
	}
	if soloErr.Code != output.ErrNoActiveSession {
		t.Errorf("expected NO_ACTIVE_SESSION, got %s", soloErr.Code)
	}
}

func TestRenewReservation(t *testing.T) {
	database := testDB(t)
	task := createReadyTask(t, database)
	db.StartSession(database, task.ID, "worker1", 1, 3600)

	res, err := db.RenewReservation(database, task.ID)
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if !res.Active {
		t.Error("expected active reservation")
	}
	if res.ExpiresAt == "" {
		t.Error("expected expires_at after renewal")
	}
}

func TestRenewNoReservation(t *testing.T) {
	database := testDB(t)

	_, err := db.RenewReservation(database, "T-1")
	if err == nil {
		t.Error("expected error for no active reservation")
	}
}

func TestListSessions(t *testing.T) {
	database := testDB(t)
	task := createReadyTask(t, database)
	db.StartSession(database, task.ID, "worker1", 1, 3600)

	sessions, err := db.ListSessions(database, "", "", false)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Errorf("expected 1 session, got %d", len(sessions))
	}

	// Filter by worker
	sessions, _ = db.ListSessions(database, "", "worker1", false)
	if len(sessions) != 1 {
		t.Errorf("expected 1 session for worker1, got %d", len(sessions))
	}

	sessions, _ = db.ListSessions(database, "", "nonexistent", false)
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions for nonexistent worker, got %d", len(sessions))
	}
}

func TestGetActiveReservation(t *testing.T) {
	database := testDB(t)
	task := createReadyTask(t, database)
	db.StartSession(database, task.ID, "worker1", 1, 3600)

	res, err := db.GetActiveReservation(database, task.ID)
	if err != nil {
		t.Fatalf("get active res: %v", err)
	}
	if res == nil {
		t.Fatal("expected reservation")
	}
	if !res.Active {
		t.Error("expected active")
	}
}

func TestGetActiveReservationNone(t *testing.T) {
	database := testDB(t)

	res, err := db.GetActiveReservation(database, "T-999")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res != nil {
		t.Error("expected nil for no reservation")
	}
}

func TestLazyZombieScan(t *testing.T) {
	database := testDB(t)

	// Just verify it doesn't panic or deadlock on an empty DB
	db.LazyZombieScan(database)
}
