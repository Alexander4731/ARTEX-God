package db

import (
	"encoding/json"
	"testing"

	"github.com/Autumn-27/artex/notify"
	"github.com/Autumn-27/artex/sidequestion"
)

func TestBinaryPersistence(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	const input = "PNG\x00证据 literal \\u0000 invalid \xff"
	const want = "PNG证据 literal \\u0000 invalid �"
	raw, err := json.Marshal(map[string]any{"sample": input})
	if err != nil {
		t.Fatal(err)
	}
	check := func(t *testing.T, query string, args ...any) {
		t.Helper()
		var got string
		if err := d.QueryRow(query, args...).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("persisted value = %q, want %q", got, want)
		}
	}

	t.Run("approval creation and decisions", func(t *testing.T) {
		for _, decided := range []bool{false, true} {
			audit := &InterceptAudit{UserMessage: input, ModelInput: raw, Correlation: "exact", ExecutionStatus: "unknown"}
			var id int64
			var err error
			if decided {
				id, err = d.CreateDecidedIntercept(0, 0, "binary-test", "worker", "Bash", raw, "allowed", input, audit)
			} else {
				id, err = d.CreateInterceptPending(0, 0, "binary-test", "worker", "Bash", raw, input, audit)
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { d.Exec(`DELETE FROM intercept_pending WHERE id=$1`, id) })
			check(t, `SELECT tool_input->>'sample' FROM intercept_pending WHERE id=$1`, id)
			check(t, `SELECT reason FROM intercept_pending WHERE id=$1`, id)
			check(t, `SELECT audit->>'user_message' FROM intercept_pending WHERE id=$1`, id)
			check(t, `SELECT audit->'model_input'->>'sample' FROM intercept_pending WHERE id=$1`, id)
			if decided {
				if ok, err := d.ResolveUnknownExecution(t.Context(), id, "succeeded", input); err != nil || !ok {
					t.Fatalf("resolve result: %v %v", ok, err)
				}
				check(t, `SELECT audit->>'output' FROM intercept_pending WHERE id=$1`, id)
			} else {
				if ok, err := d.ResolveIntercept(id, "allowed", "allow", input); err != nil || !ok {
					t.Fatalf("resolve approval: %v %v", ok, err)
				}
				check(t, `SELECT audit->>'decision_reason' FROM intercept_pending WHERE id=$1`, id)
			}
		}
	})

	task, err := d.CreateTask("binary persistence", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.DeleteTask(task.ID) })
	es := d.Exploration(task.ExplorationID)
	t.Run("graph and activity", func(t *testing.T) {
		id, err := es.AddGoal(map[string]any{"text": input}, "human")
		if err != nil {
			t.Fatal(err)
		}
		check(t, `SELECT payload->>'text' FROM exploration_nodes WHERE id=$1`, id)
		if err := es.UpdateGoalPayload(id, input, "test"); err != nil {
			t.Fatal(err)
		}
		check(t, `SELECT payload->>'text' FROM exploration_nodes WHERE id=$1`, id)
		digest, err := es.AddDigest(map[string]any{"sample": input}, []int64{id})
		if err != nil {
			t.Fatal(err)
		}
		check(t, `SELECT payload->>'sample' FROM exploration_nodes WHERE id=$1`, digest)
		activity, err := es.AppendActivity(Activity{Kind: "tool_result", Detail: input, Metadata: raw})
		if err != nil {
			t.Fatal(err)
		}
		check(t, `SELECT metadata->>'sample' FROM activity WHERE id=$1`, activity)
		check(t, `SELECT detail FROM activity WHERE id=$1`, activity)
	})

	t.Run("finding transaction and notifications", func(t *testing.T) {
		finding, err := es.RecordFinding(t.Context(), RecordFindingInput{TaskID: task.ID, VulnClass: "test", Name: input, Severity: "high", Summary: input, Evidence: input, Worker: "worker"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { d.Exec(`DELETE FROM notification_events WHERE finding_id=$1`, finding.FindingID) })
		for _, column := range []string{"name", "summary", "evidence"} {
			check(t, `SELECT `+column+` FROM findings WHERE id=$1`, finding.FindingID)
		}
		check(t, `SELECT payload->'evidence'->>'poc' FROM exploration_nodes WHERE id=$1`, finding.NodeID)
		check(t, `SELECT snapshot->>'summary' FROM notification_events WHERE finding_id=$1`, finding.FindingID)
		event, err := d.AddNotificationEvent(t.Context(), notify.EventFindingStatusChanged, finding.FindingID, notify.Snapshot{Summary: input})
		if err != nil {
			t.Fatal(err)
		}
		check(t, `SELECT snapshot->>'summary' FROM notification_events WHERE id=$1`, event)
		intent, activity, err := es.AddFindingFollowUpIntent(finding.FindingID, finding.NodeID, input, Activity{Kind: "text", Metadata: raw})
		if err != nil {
			t.Fatal(err)
		}
		check(t, `SELECT payload->>'summary' FROM exploration_nodes WHERE id=$1`, intent)
		check(t, `SELECT metadata->>'sample' FROM activity WHERE id=$1`, activity.ID)
		var metadata map[string]string
		if err := json.Unmarshal(activity.Metadata, &metadata); err != nil || metadata["sample"] != want {
			t.Fatalf("returned activity metadata: %v %v", metadata, err)
		}
		legacy, err := d.AddFinding(task.ID, 0, "test", input, "high", input, input, "worker", nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, column := range []string{"name", "summary", "evidence"} {
			check(t, `SELECT `+column+` FROM findings WHERE id=$1`, legacy)
		}
	})

	t.Run("asset JSON array", func(t *testing.T) {
		array, err := marshalJSONBArray([]map[string]any{{"sample": input}, {"sample": "unchanged"}})
		if err != nil {
			t.Fatal(err)
		}
		check(t, `SELECT ($1::jsonb[])[1]->>'sample'`, array)
		var second string
		if err := d.QueryRow(`SELECT ($1::jsonb[])[2]->>'sample'`, array).Scan(&second); err != nil || second != "unchanged" {
			t.Fatalf("second array item: %q %v", second, err)
		}
	})
}

func TestSideBinaryPersistence(t *testing.T) {
	d, snapshot := sideFixture(t)
	e, _, err := d.StartSideRequest(t.Context(), snapshot, "binary", "question")
	if err != nil {
		t.Fatal(err)
	}
	const input = "answer\x00中文 literal \\u0000 invalid \xff"
	const want = "answer中文 literal \\u0000 invalid �"
	if err := d.SaveSideMemory(t.Context(), *e, sidequestion.Memory{History: input, SnapshotSummary: input}); err != nil {
		t.Fatal(err)
	}
	memory, err := d.SideMemory(t.Context(), *e)
	if err != nil || memory.History != want || memory.SnapshotSummary != want {
		t.Fatalf("memory: %+v %v", memory, err)
	}
	e.Answer, e.Error, e.Status, e.Sequence = input, input, "failed", 1
	if ok, err := d.UpdateSideRequest(t.Context(), *e); err != nil || !ok {
		t.Fatalf("update: %v %v", ok, err)
	}
	got, err := d.SideRequest(t.Context(), e.ID)
	if err != nil || got.Answer != want || got.Error != want || got.Status != "failed" {
		t.Fatalf("request: %+v %v", got, err)
	}
}
