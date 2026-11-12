package db

import (
	"strings"
	"testing"
)

func queueFollowup(t *testing.T, d *DB, taskID string, id int64) *ExecutionRecord {
	t.Helper()
	if err := d.CompleteIntercept(id, "run", "call", "unknown", "tool execution interrupted", false); err != nil {
		t.Fatal(err)
	}
	if err := d.QueueExecutionRecoveries(t.Context(), taskID); err != nil {
		t.Fatal(err)
	}
	e, err := d.ClaimExecutionRecovery(t.Context(), taskID)
	if err != nil || e == nil {
		t.Fatal("claim", err)
	}
	return e
}

func TestExpiredRecoveryLeaseCannotCommit(t *testing.T) {
	d, taskID, id := recoveryDBFixture(t)
	e := queueFollowup(t, d, taskID, id)
	if _, err := d.Exec(`UPDATE execution_recoveries SET lease_until=now()-interval '1 second' WHERE approval_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if won, err := d.CommitRecovery(t.Context(), *e, "resolved", "achieved", "model", []byte(`{}`)); err != nil || won {
		t.Fatalf("expired verifier accepted: won=%v err=%v", won, err)
	}
}

func TestLateReceiptInvalidatesDisplayedHumanDecision(t *testing.T) {
	d, taskID, id := recoveryDBFixture(t)
	e := queueFollowup(t, d, taskID, id)
	if won, err := d.CommitRecovery(t.Context(), *e, "resolved", "partial", "model", []byte(`{}`)); err != nil || !won {
		t.Fatal(won, err)
	}
	displayed, err := d.GetExecution(t.Context(), taskID, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.StoreExecutionEvent(t.Context(), id, "process_exited", "succeeded", "new real result", false, []byte(`{"kind":"process_exited","exit_code":0}`)); err != nil {
		t.Fatal(err)
	}
	fresh, err := d.GetExecution(t.Context(), taskID, id)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Version == displayed.Version {
		t.Error("late receipt left the UI version unchanged")
	}
	if won, err := d.DecideExecutionRecovery(t.Context(), displayed, "park", "decision based on old output"); err != nil || won {
		t.Errorf("stale human decision accepted: won=%v err=%v", won, err)
	}
}

func TestRecoverySummaryDeliversActualLateOutput(t *testing.T) {
	d, taskID, id := recoveryDBFixture(t)
	_ = queueFollowup(t, d, taskID, id)
	if err := d.StoreExecutionEvent(t.Context(), id, "process_exited", "failed", "fixture: 18 rows complete, 2 unfinished", false, []byte(`{"kind":"process_exited","exit_code":2}`)); err != nil {
		t.Fatal(err)
	}
	e, err := d.ClaimExecutionRecovery(t.Context(), taskID)
	if err != nil || e == nil {
		t.Fatal(err)
	}
	if won, err := d.CommitRecovery(t.Context(), *e, "resolved", "indeterminate", "executor", []byte(`{"verdict":{"decision":"continue_with_result"}}`)); err != nil || !won {
		t.Fatal(won, err)
	}
	summary := d.RecoverySummary(t.Context(), taskID, 7)
	if !strings.Contains(summary, "18 rows complete") {
		t.Fatalf("worker only receives a decision, not the result: %s", summary)
	}
}

func TestRecoveryPaginationReachesOldPendingRecord(t *testing.T) {
	d, taskID, oldest := recoveryDBFixture(t)
	_ = queueFollowup(t, d, taskID, oldest)
	for n := 0; n < 100; n++ {
		a := &InterceptAudit{RunID: "extra", ToolUseID: "extra", InputDigest: "fixture", EffectiveAction: "allow", ExecutionStatus: "unknown"}
		if _, err := d.CreateDecidedIntercept(0, 0, taskID, "worker", "Read", []byte(`{}`), "allowed", "fixture", a); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.QueueExecutionRecoveries(t.Context(), taskID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE execution_recoveries r SET state='resolved' FROM intercept_pending ip WHERE r.approval_id=ip.id AND ip.task_id=$1 AND ip.id<>$2`, taskID, oldest); err != nil {
		t.Fatal(err)
	}
	first, err := d.ListExecutionRecoveries(t.Context(), taskID)
	if err != nil || len(first) != 100 {
		t.Fatal(len(first), err)
	}
	second, err := d.ListExecutionRecoveries(t.Context(), taskID, first[len(first)-1].ID)
	if err != nil || len(second) != 1 || second[0].ID != oldest || second[0].RecoveryState != "checking" {
		t.Fatal("older unresolved execution inaccessible", second, err)
	}
}
