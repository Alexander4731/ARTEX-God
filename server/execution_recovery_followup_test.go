package server

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
)

func TestRecoveryTimeoutConsumesBudgetAndStopsRetrying(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	task, err := m.CreateTask("timeout recovery", "fixture", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var tid int64
	fmt.Sscan(task.ID, &tid)
	defer m.pg.DeleteTask(tid)
	if err := m.pg.SetExecutionPolicy(t.Context(), tid, "auto"); err != nil {
		t.Fatal(err)
	}
	if err := m.pg.SetPaused(tid, false); err != nil {
		t.Fatal(err)
	}
	audit := &db.InterceptAudit{RunID: "run", ToolUseID: "call", InputDigest: "fixture", EffectiveAction: "allow", ExecutionStatus: "unknown"}
	id, err := m.pg.CreateDecidedIntercept(0, 0, task.ID, "worker", "Bash", []byte(`{}`), "allowed", "fixture", audit)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.pg.QueueExecutionRecoveries(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	s := newAdmissionTestServer(m, func(*Task) bool { return false })
	for attempt := 0; attempt < 2; attempt++ {
		e, err := m.pg.ClaimExecutionRecovery(t.Context(), task.ID)
		if err != nil || e == nil {
			t.Fatal(err)
		}
		timeout, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
		err = s.deferFailedExecutionRecovery(timeout, *e, context.DeadlineExceeded)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := m.pg.Exec(`UPDATE execution_recoveries SET next_check_at=now() WHERE approval_id=$1`, id); err != nil {
			t.Fatal(err)
		}
	}
	s.recoverTaskExecutions(t.Context(), task)
	e, err := m.pg.GetExecution(t.Context(), task.ID, id)
	if err != nil || e.Attempts != 2 || e.RecoveryState != "waiting_input" {
		t.Fatalf("timeouts retry indefinitely: %+v err=%v", e, err)
	}
}
