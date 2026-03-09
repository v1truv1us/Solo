package db_test

import (
	"testing"

	"github.com/v1truv1us/solo/internal/db"
	"github.com/v1truv1us/solo/internal/output"
)

func TestRecoverTask(t *testing.T) {
	database := testDB(t)
	task := createReadyTask(t, database)
	session, _ := db.StartSession(database, task.ID, "worker1", 1, 3600)

	// Get current version
	got, _ := db.GetTask(database, task.ID)

	result, err := db.RecoverTask(database, task.ID, got.Version, nil, nil)
	if err != nil {
		t.Fatalf("recover task: %v", err)
	}
	if !result.Recovered {
		t.Error("expected recovered=true")
	}
	if result.PreviousStatus != "in_progress" {
		t.Errorf("expected previous_status=in_progress, got %q", result.PreviousStatus)
	}
	if result.RecoveredTo != "ready" {
		t.Errorf("expected recovered_to=ready, got %q", result.RecoveredTo)
	}
	if result.SessionEnded == nil || *result.SessionEnded != session.SessionID {
		t.Error("expected session_ended to match")
	}
	if result.ReservationReleased == nil {
		t.Error("expected reservation_released")
	}
	if result.RecoveryRecordID == 0 {
		t.Error("expected recovery_record_id > 0")
	}

	// Verify task is ready
	recovered, _ := db.GetTask(database, task.ID)
	if recovered.Status != "ready" {
		t.Errorf("expected ready, got %q", recovered.Status)
	}

	// Verify reservation released
	res, _ := db.GetActiveReservation(database, task.ID)
	if res != nil {
		t.Error("expected no active reservation after recovery")
	}
}

func TestRecoverTaskOCCConflict(t *testing.T) {
	database := testDB(t)
	task := createReadyTask(t, database)
	db.StartSession(database, task.ID, "worker1", 1, 3600)

	// Use wrong version
	_, err := db.RecoverTask(database, task.ID, 999, nil, nil)
	if err == nil {
		t.Error("expected VERSION_CONFLICT")
	}
	soloErr, ok := err.(*output.SoloError)
	if !ok {
		t.Fatalf("expected SoloError, got %T", err)
	}
	if soloErr.Code != output.ErrVersionConflict {
		t.Errorf("expected VERSION_CONFLICT, got %s", soloErr.Code)
	}
}

func TestRecoverTaskNotFound(t *testing.T) {
	database := testDB(t)

	_, err := db.RecoverTask(database, "T-999", 1, nil, nil)
	if err == nil {
		t.Error("expected TASK_NOT_FOUND")
	}
}

func TestRecoverAll(t *testing.T) {
	database := testDB(t)

	// No zombies to recover
	result, err := db.RecoverAll(database)
	if err != nil {
		t.Fatalf("recover all: %v", err)
	}
	if result.Recovered != 0 {
		t.Errorf("expected 0 recovered, got %d", result.Recovered)
	}
	if result.Records == nil {
		t.Error("records should not be nil")
	}
}

func TestRecoverAllWithZombie(t *testing.T) {
	database := testDB(t)
	task := createReadyTask(t, database)

	// Start session with a PID that definitely doesn't exist
	_, err := db.StartSession(database, task.ID, "worker1", 99999999, 3600)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	result, err := db.RecoverAll(database)
	if err != nil {
		t.Fatalf("recover all: %v", err)
	}
	if result.Scanned != 1 {
		t.Errorf("expected 1 scanned, got %d", result.Scanned)
	}
	// PID 99999999 should be dead
	if result.Recovered != 1 {
		t.Errorf("expected 1 recovered, got %d", result.Recovered)
	}
}
