package db

import (
	"fmt"
	"testing"
)

func TestInterruptedIntentWaitsForExecutionVerification(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	task, err := d.CreateTask("restart fixture", "verify interrupted worker", nil, 0, 600)
	if err != nil {
		t.Fatal(err)
	}
	taskID := fmt.Sprint(task.ID)
	t.Cleanup(func() {
		_, _ = d.Exec(`DELETE FROM intercept_pending WHERE task_id=$1`, taskID)
		_ = d.DeleteTask(task.ID)
	})
	s := d.Exploration(task.ExplorationID)
	intent, err := s.AddIntent(map[string]any{"summary": "interrupted"}, 1, nil, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetIntentState(intent, "running"); err != nil {
		t.Fatal(err)
	}
	id, err := d.CreateDecidedIntercept(0, 0, taskID, "fixture", "Probe", []byte(`{}`), "allowed", "fixture", &InterceptAudit{IntentID: intent, ExecutionStatus: "awaiting_result"})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := s.ResetRunningIntents(); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if err := d.ReopenExecutionPausedIntents(taskID); err != nil {
		t.Fatal(err)
	}
	if n, err := s.GetNode(intent); err != nil || n.State != "paused" || n.BlockedReason != IntentBlockedExecutionUnknown {
		t.Fatalf("lost interrupted worker barrier: %+v %v", n, err)
	}
	if err := d.FinishIntentExecutionAudit(taskID, intent); err != nil {
		t.Fatal(err)
	}
	if won, err := d.ResolveUnknownExecution(t.Context(), id, "failed", "checked local fixture"); err != nil || !won {
		t.Fatal(won, err)
	}
	if err := d.ReopenExecutionPausedIntents(taskID); err != nil {
		t.Fatal(err)
	}
	if n, err := s.GetNode(intent); err != nil || n.State != "open" || n.BlockedReason != "" {
		t.Fatalf("verified worker stranded: %+v %v", n, err)
	}
}
