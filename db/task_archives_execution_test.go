package db

import (
	"fmt"
	"strconv"
	"testing"
)

func TestTaskArchivePreservesExecutionLedger(t *testing.T) {
	d, taskID, id := recoveryDBFixture(t)
	numeric, _ := strconv.ParseInt(taskID, 10, 64)
	for _, ensure := range []func() error{d.EnsureLLMRecordsTable, d.EnsureLLMUsageTable} {
		if err := ensure(); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.StoreExecutionEvent(t.Context(), id, "process_exited", "succeeded", "原始执行结果", false, []byte(`{"kind":"process_exited","exit_code":0}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO execution_recoveries(approval_id,state,claim_token,lease_until,version) VALUES($1,'checking','old-claim',now()+interval '1 hour',9)`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO execution_recovery_decisions(approval_id,source,evidence_version,decision) VALUES($1,'human',1,'{"reason":"history"}')`, id); err != nil {
		t.Fatal(err)
	}
	if err := d.SetTaskToolPolicy(t.Context(), numeric, TaskToolPolicy{AllowAll: true}); err != nil {
		t.Fatal(err)
	}
	if err := d.SetPaused(numeric, true); err != nil {
		t.Fatal(err)
	}
	before, err := d.GetExecution(t.Context(), taskID, id)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := d.QueueTaskArchive(numeric)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Exec("DELETE FROM task_archives WHERE task_id=$1", numeric) })
	claimed, err := d.ClaimTaskArchiveJob(t.Context())
	if err != nil || claimed == nil || claimed.ID != archive.ID {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	snapshot, err := d.SnapshotTaskArchive(numeric)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.CompleteTaskArchive(archive.ID, snapshot, "fixture.tar.zst", "fixture", 100, 50); err != nil {
		t.Fatal(err)
	}
	// Simulate restore without depending on policy rows remaining in the hot stub.
	d.Exec("DELETE FROM task_execution_policies WHERE task_id=$1", numeric)
	d.Exec("DELETE FROM task_tool_policies WHERE task_id=$1", numeric)
	if _, err = d.QueueTaskArchiveRestore(archive.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = d.ClaimTaskArchiveJob(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err = d.RestoreTaskArchive(archive.ID, snapshot, 0); err != nil {
		t.Fatal(err)
	}
	after, err := d.GetExecution(t.Context(), taskID, id)
	if err != nil {
		t.Fatal(err)
	}
	if after.ExecutionState != before.ExecutionState || after.Output != before.Output || after.EvidenceVersion != before.EvidenceVersion {
		t.Fatalf("executor facts lost: before=%+v after=%+v", before, after)
	}
	if after.ClaimToken != "" || after.RecoveryState != "parked" || after.Version <= before.Version {
		t.Fatalf("old claim resurrected: %+v", after)
	}
	if after.ControlVersion <= before.ControlVersion {
		t.Fatal("restore did not fence old task operations")
	}
	for _, table := range []string{"tool_execution_events", "execution_recovery_decisions"} {
		var count int
		if err = d.QueryRow("SELECT count(*) FROM "+table+" WHERE approval_id=$1", id).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s: count=%d err=%v", table, count, err)
		}
	}
	policy, err := d.ExecutionPolicy(t.Context(), taskID)
	if err != nil || policy.Mode != "auto" {
		t.Fatalf("execution policy: %+v %v", policy, err)
	}
	toolPolicy, err := d.GetTaskToolPolicy(t.Context(), numeric)
	if err != nil || !toolPolicy.AllowAll {
		t.Fatalf("tool policy: %+v %v", toolPolicy, err)
	}
	if err = d.CompleteTaskArchiveRestore(archive.ID); err != nil {
		t.Fatal(err)
	}
	// Restored executor identity must continue rejecting a second dispatch.
	if err = d.PrepareToolExecution(t.Context(), id, "run", "call", "", false); err == nil {
		t.Fatal(fmt.Sprint("replayed execution ", id))
	}
}

func TestTaskArchiveRejectsLateExecutionEvidence(t *testing.T) {
	d, taskID, id := recoveryDBFixture(t)
	numeric, _ := strconv.ParseInt(taskID, 10, 64)
	for _, ensure := range []func() error{d.EnsureLLMRecordsTable, d.EnsureLLMUsageTable} {
		if err := ensure(); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.SetPaused(numeric, true); err != nil {
		t.Fatal(err)
	}
	a, err := d.QueueTaskArchive(numeric)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Exec("DELETE FROM task_archives WHERE task_id=$1", numeric) })
	if _, err = d.ClaimTaskArchiveJob(t.Context()); err != nil {
		t.Fatal(err)
	}
	snapshot, err := d.SnapshotTaskArchive(numeric)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.StoreExecutionEvent(t.Context(), id, "process_exited", "failed", "late output", false, []byte(`{"kind":"process_exited","exit_code":2}`)); err != nil {
		t.Fatal(err)
	}
	if err = d.CompleteTaskArchive(a.ID, snapshot, "fixture", "fixture", 1, 1); err == nil {
		t.Fatal("stale archive deleted newer evidence")
	}
	var n int
	if err = d.QueryRow("SELECT count(*) FROM tool_execution_receipts WHERE approval_id=$1 AND output='late output'", id).Scan(&n); err != nil || n != 1 {
		t.Fatalf("late receipt was lost: %d %v", n, err)
	}
	delete(snapshot.Tables, "tool_execution_events")
	if err = ValidateArchiveExecutions(snapshot); err == nil {
		t.Fatal("accepted incomplete v4")
	}
}
