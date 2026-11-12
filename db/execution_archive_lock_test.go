package db

import (
	"context"
	"testing"
	"time"
)

func TestRecoveryCommitDoesNotHoldReceiptWhileWaitingForTask(t *testing.T) {
	d, taskID, id := recoveryDBFixture(t)
	e := queueFollowup(t, d, taskID, id)
	tx, err := d.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("SELECT id FROM tasks WHERE id::text=$1 FOR UPDATE", taskID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := d.CommitRecovery(ctx, *e, "resolved", "achieved", "model", []byte(`{}`))
		done <- err
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var waiting bool
		err = d.QueryRow("SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE 'SELECT execution_control_version FROM tasks%')").Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("recovery did not reach task lock")
		}
		time.Sleep(time.Millisecond * 10)
	}
	if _, err := tx.Exec("SELECT approval_id FROM tool_execution_receipts WHERE approval_id=$1 FOR UPDATE NOWAIT", id); err != nil {
		t.Fatalf("receipt locked before task: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
