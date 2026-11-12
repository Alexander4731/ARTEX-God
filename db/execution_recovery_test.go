package db

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

func recoveryDBFixture(t *testing.T) (*DB, string, int64) {
	t.Helper()
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	task, err := d.CreateTask(t.Name(), "local recovery fixture", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.DeleteTask(task.ID) })
	taskID := fmt.Sprint(task.ID)
	if err = d.SetPaused(task.ID, false); err != nil {
		t.Fatal(err)
	}
	if err = d.SetExecutionPolicy(t.Context(), task.ID, "auto"); err != nil {
		t.Fatal(err)
	}
	a := &InterceptAudit{RunID: "run", ToolUseID: "call", IntentID: 7, InputDigest: "fixture-digest", Correlation: "exact", EffectiveAction: "allow", ExecutionStatus: "awaiting_result"}
	id, err := d.CreateDecidedIntercept(0, 0, taskID, "worker", "Bash", []byte(`{"command":"echo fixture"}`), "allowed", "fixture", a)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.PrepareToolExecution(t.Context(), id, "run", "call", t.TempDir(), false); err != nil {
		t.Fatal(err)
	}
	return d, taskID, id
}

func TestDurableLateExecutionReceipt(t *testing.T) {
	d, taskID, id := recoveryDBFixture(t)
	ctx := t.Context()
	if err := d.StoreExecutionEvent(ctx, id, "process_started", "running", "", false, []byte(`{"process_id":123}`)); err != nil {
		t.Fatal(err)
	}
	if err := d.StoreExecutionEvent(ctx, id, "returned", "succeeded", "background launch acknowledgement", false, []byte(`{"kind":"returned"}`)); err != nil {
		t.Fatal(err)
	}
	e, err := d.GetExecution(ctx, taskID, id)
	if err != nil || e.ExecutionState != "running" {
		t.Fatalf("launch ack settled process: %+v %v", e, err)
	}
	if err = d.CompleteIntercept(id, "run", "call", "unknown", "tool execution interrupted", false); err != nil {
		t.Fatal(err)
	}
	if err = d.QueueExecutionRecoveries(ctx, taskID); err != nil {
		t.Fatal(err)
	}
	old, err := d.ClaimExecutionRecovery(ctx, taskID)
	if err != nil || old == nil {
		t.Fatal(err)
	}
	payload := json.RawMessage(`{"kind":"process_exited","exit_code":-1,"cancel_cause":"user_pause"}`)
	if err = d.StoreExecutionEvent(ctx, id, "process_exited", "canceled", "partial fixture output", false, payload); err != nil {
		t.Fatal(err)
	}
	if won, err := d.CommitRecovery(ctx, *old, "resolved", "achieved", "model", []byte(`{}`)); err != nil || won {
		t.Fatalf("stale verifier won: %v %v", won, err)
	}
	detail, err := d.GetInterceptDetail(id)
	if err != nil || detail.Audit.ExecutionStatus != "failed" || detail.Audit.Output != "partial fixture output" {
		t.Fatalf("late result not recovered: %+v %v", detail, err)
	}
	e, _ = d.GetExecution(ctx, taskID, id)
	version := e.EvidenceVersion
	if err = d.StoreExecutionEvent(ctx, id, "process_exited", "canceled", "partial fixture output", false, payload); err != nil {
		t.Fatal(err)
	}
	e, _ = d.GetExecution(ctx, taskID, id)
	if e.EvidenceVersion != version {
		t.Fatal("duplicate receipt changed version")
	}
	if err = d.PrepareToolExecution(ctx, id, "run", "call", "", false); err == nil {
		t.Fatal("duplicate dispatch admitted")
	}
}

func TestRecoveryPauseFenceAndHumanEvidence(t *testing.T) {
	d, taskID, id := recoveryDBFixture(t)
	ctx := t.Context()
	if err := d.CompleteIntercept(id, "run", "call", "unknown", "original output", false); err != nil {
		t.Fatal(err)
	}
	if err := d.QueueExecutionRecoveries(ctx, taskID); err != nil {
		t.Fatal(err)
	}
	e, err := d.ClaimExecutionRecovery(ctx, taskID)
	if err != nil || e == nil {
		t.Fatal(err)
	}
	var numeric int64
	fmt.Sscan(taskID, &numeric)
	if err = d.SetPaused(numeric, true); err != nil {
		t.Fatal(err)
	}
	if next, err := d.ClaimExecutionRecovery(ctx, taskID); err != nil || next != nil {
		t.Fatal("paused task claimed", err)
	}
	if err = d.SetPaused(numeric, false); err != nil {
		t.Fatal(err)
	}
	if won, err := d.CommitRecovery(ctx, *e, "resolved", "partial", "model", []byte(`{}`)); err != nil || won {
		t.Fatal("pre-pause result applied after resume", err)
	}
	fresh, _ := d.GetExecution(ctx, taskID, id)
	if won, err := d.DecideExecutionRecovery(ctx, fresh, "accept_partial", "checked available output"); err != nil || !won {
		t.Fatal(won, err)
	}
	detail, _ := d.GetInterceptDetail(id)
	if detail.Audit.Output != "original output" || detail.Audit.ExecutionStatus != "unknown" {
		t.Fatal("human decision overwrote executor evidence")
	}
	if won, _ := d.DecideExecutionRecovery(ctx, fresh, "park", "stale choice"); won {
		t.Fatal("duplicate human decision accepted")
	}
}

func TestProcessExitOutranksOutOfOrderLaunchReceipt(t *testing.T) {
	d, taskID, id := recoveryDBFixture(t)
	ctx := t.Context()
	if err := d.StoreExecutionEvent(ctx, id, "returned", "succeeded", "background launch", false, []byte(`{"kind":"returned"}`)); err != nil {
		t.Fatal(err)
	}
	if err := d.StoreExecutionEvent(ctx, id, "process_exited", "failed", "actual failure", false, []byte(`{"kind":"process_exited","exit_code":2}`)); err != nil {
		t.Fatal(err)
	}
	if err := d.StoreExecutionEvent(ctx, id, "process_started", "running", "", false, []byte(`{"kind":"process_started","process_id":123}`)); err != nil {
		t.Fatal(err)
	}
	e, err := d.GetExecution(ctx, taskID, id)
	if err != nil || e.ExecutionState != "failed" || e.Output != "actual failure" {
		t.Fatal(e, err)
	}
	detail, _ := d.GetInterceptDetail(id)
	if detail.Audit.ExecutionStatus != "failed" {
		t.Fatal("launch acknowledgement survived stronger evidence")
	}
}

func TestRecoveryTaskAndIntentIsolation(t *testing.T) {
	d, taskID, id := recoveryDBFixture(t)
	ctx := context.Background()
	if err := d.CompleteIntercept(id, "run", "call", "unknown", "interrupted", false); err != nil {
		t.Fatal(err)
	}
	if err := d.QueueExecutionRecoveries(ctx, taskID); err != nil {
		t.Fatal(err)
	}
	if blocked, err := d.HasUnknownIntentExecution(ctx, taskID, 7); err != nil || !blocked {
		t.Fatal(blocked, err)
	}
	if blocked, err := d.HasUnknownIntentExecution(ctx, taskID, 8); err != nil || blocked {
		t.Fatal("unrelated intent blocked", err)
	}
	if blocked, err := d.HasUncertainWrites(ctx, taskID); err != nil || !blocked {
		t.Fatal("uncertain shell write not fenced", err)
	}
	if rows, err := d.ListActionableIntercepts(taskID); err != nil || len(rows) != 0 {
		t.Fatal("recovery mixed into permission approvals", err)
	}
	other, err := d.CreateTask("other recovery task", "fixture", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(other.ID)
	if p, err := d.ExecutionPolicy(ctx, fmt.Sprint(other.ID)); err != nil || p.Mode != "legacy" {
		t.Fatal("policy leaked", err)
	}
	if blocked, err := d.HasUncertainWrites(ctx, fmt.Sprint(other.ID)); err != nil || blocked {
		t.Fatal("task barrier leaked", err)
	}
	if _, err = d.GetExecution(ctx, fmt.Sprint(other.ID), id); err == nil {
		t.Fatal("execution visible in wrong task")
	}
}

func TestRecoveryPolicyChangeFencesOutstandingDecision(t *testing.T) {
	d, taskID, id := recoveryDBFixture(t)
	ctx := t.Context()
	if err := d.CompleteIntercept(id, "run", "call", "unknown", "interrupted", false); err != nil {
		t.Fatal(err)
	}
	if err := d.QueueExecutionRecoveries(ctx, taskID); err != nil {
		t.Fatal(err)
	}
	e, err := d.ClaimExecutionRecovery(ctx, taskID)
	if err != nil || e == nil {
		t.Fatal(err)
	}
	var numeric int64
	fmt.Sscan(taskID, &numeric)
	if err := d.SetExecutionPolicy(ctx, numeric, "legacy"); err != nil {
		t.Fatal(err)
	}
	if won, err := d.CommitRecovery(ctx, *e, "resolved", "partial", "model", []byte(`{}`)); err != nil || won {
		t.Fatal("disabled policy accepted old verifier", won, err)
	}
	if err := d.DeferExecutionRecovery(ctx, *e, "fixture", false); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE execution_recoveries SET next_check_at=now() WHERE approval_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if next, err := d.ClaimExecutionRecovery(ctx, taskID); err != nil || next != nil {
		t.Fatal("disabled policy claimed work", err)
	}
}

func TestUncertainWriteRemainsVisibleAfterEffectDecision(t *testing.T) {
	d, taskID, id := recoveryDBFixture(t)
	ctx := t.Context()
	if err := d.CompleteIntercept(id, "run", "call", "unknown", "interrupted", false); err != nil {
		t.Fatal(err)
	}
	if err := d.QueueExecutionRecoveries(ctx, taskID); err != nil {
		t.Fatal(err)
	}
	e, err := d.ClaimExecutionRecovery(ctx, taskID)
	if err != nil || e == nil {
		t.Fatal(err)
	}
	if won, err := d.CommitRecovery(ctx, *e, "resolved", "achieved", "model", []byte(`{"verdict":{"decision":"continue"}}`)); err != nil || !won {
		t.Fatal(won, err)
	}
	rows, err := d.ListExecutionRecoveries(ctx, taskID)
	if err != nil || len(rows) != 1 || !rows[0].WriteBlocked {
		t.Fatal("remaining write barrier hidden", rows, err)
	}
	if won, err := d.DecideExecutionRecovery(ctx, rows[0], "park", "verified no further writes; abandon branch"); err != nil || !won {
		t.Fatal(won, err)
	}
	fresh, err := d.GetExecution(ctx, taskID, id)
	blocked, blockErr := d.HasUncertainWrites(ctx, taskID)
	if err != nil || blockErr != nil || fresh.WriteBlocked || blocked {
		t.Fatal("human decision not reflected", fresh, blocked, err, blockErr)
	}
}
