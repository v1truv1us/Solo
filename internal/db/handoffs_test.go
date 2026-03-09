package db_test

import (
	"testing"

	"github.com/v1truv1us/solo/internal/db"
	"github.com/v1truv1us/solo/internal/output"
)

func TestCreateHandoff(t *testing.T) {
	database := testDB(t)
	task := createReadyTask(t, database)
	db.StartSession(database, task.ID, "worker1", 1, 3600)

	result, err := db.CreateHandoff(database, db.CreateHandoffParams{
		TaskID:        task.ID,
		Summary:       "Handing off to next agent",
		RemainingWork: "Tests needed",
		ToWorker:      "worker2",
		Files:         []string{"src/main.go"},
	})
	if err != nil {
		t.Fatalf("create handoff: %v", err)
	}
	if result.HandoffID == "" {
		t.Error("expected handoff ID")
	}
	if result.FromWorker != "worker1" {
		t.Errorf("expected from_worker=worker1, got %q", result.FromWorker)
	}
	if result.TaskStatus != "ready" {
		t.Errorf("expected task status ready, got %q", result.TaskStatus)
	}
	if !result.SessionEnded {
		t.Error("expected session_ended=true")
	}
	if !result.ReservationReleased {
		t.Error("expected reservation_released=true")
	}

	// Verify task is ready
	got, _ := db.GetTask(database, task.ID)
	if got.Status != "ready" {
		t.Errorf("expected ready, got %q", got.Status)
	}

	// Verify reservation released
	res, _ := db.GetActiveReservation(database, task.ID)
	if res != nil {
		t.Error("expected no active reservation after handoff")
	}
}

func TestCreateHandoffNoSession(t *testing.T) {
	database := testDB(t)
	db.CreateTask(database, db.CreateTaskParams{Title: "No session"})

	_, err := db.CreateHandoff(database, db.CreateHandoffParams{
		TaskID:  "T-1",
		Summary: "Should fail",
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

func TestCreateHandoffEmptySummary(t *testing.T) {
	database := testDB(t)

	_, err := db.CreateHandoff(database, db.CreateHandoffParams{
		TaskID:  "T-1",
		Summary: "",
	})
	if err == nil {
		t.Error("expected error for empty summary")
	}
}

func TestListHandoffs(t *testing.T) {
	database := testDB(t)
	task := createReadyTask(t, database)
	db.StartSession(database, task.ID, "worker1", 1, 3600)
	db.CreateHandoff(database, db.CreateHandoffParams{
		TaskID:  task.ID,
		Summary: "First handoff",
	})

	handoffs, err := db.ListHandoffs(database, "", "")
	if err != nil {
		t.Fatalf("list handoffs: %v", err)
	}
	if len(handoffs) != 1 {
		t.Errorf("expected 1 handoff, got %d", len(handoffs))
	}

	// Filter by task
	handoffs, _ = db.ListHandoffs(database, task.ID, "")
	if len(handoffs) != 1 {
		t.Errorf("expected 1 handoff for task, got %d", len(handoffs))
	}

	// Verify JSON arrays decoded properly
	if handoffs[0].FilesTouched == nil {
		t.Error("files_touched should not be nil")
	}
}

func TestGetHandoff(t *testing.T) {
	database := testDB(t)
	task := createReadyTask(t, database)
	db.StartSession(database, task.ID, "worker1", 1, 3600)
	result, _ := db.CreateHandoff(database, db.CreateHandoffParams{
		TaskID:  task.ID,
		Summary: "Get me",
	})

	handoff, err := db.GetHandoff(database, result.HandoffID)
	if err != nil {
		t.Fatalf("get handoff: %v", err)
	}
	if handoff.ID != result.HandoffID {
		t.Errorf("ID mismatch")
	}
	if handoff.Summary != "Get me" {
		t.Errorf("summary mismatch: %q", handoff.Summary)
	}
}

func TestGetHandoffNotFound(t *testing.T) {
	database := testDB(t)

	_, err := db.GetHandoff(database, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent handoff")
	}
}
