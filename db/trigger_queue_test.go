package db

import (
	"fmt"
	"testing"
	"time"
)

func TestTriggerQueueUsesCommittedTransitions(t *testing.T) {
	d := reliabilityDB(t)
	key := fmt.Sprintf("queue-%d", time.Now().UnixNano())
	tr, err := d.CreateTrigger(&AgentTrigger{AgentKey: key, Enabled: true, OnTaskTimeout: true, TaskTimeoutMessage: "inspect"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM trigger_runs WHERE trigger_id=$1`, tr.ID); d.DeleteTrigger(tr.ID) })
	first, err := d.CreateTask("first", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(first.ID)
	second, err := d.CreateTask("second", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(second.ID)
	tx, err := d.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE tasks SET status='timeout' WHERE id=$1`, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := d.SetStatus(second.ID, "timeout"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := d.QueryRow(`SELECT COUNT(*) FROM trigger_runs WHERE trigger_id=$1`, tr.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT COUNT(*) FROM trigger_runs WHERE trigger_id=$1`, tr.ID).Scan(&count); err != nil || count != 2 {
		t.Fatal("late commit lost", count, err)
	}
	if err := d.SetStatus(first.ID, "timeout"); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT COUNT(*) FROM trigger_runs WHERE trigger_id=$1`, tr.ID).Scan(&count); err != nil || count != 2 {
		t.Fatal("repeated status generated duplicate", count, err)
	}
	tr.Enabled = false
	if err := d.UpdateTrigger(tr); err != nil {
		t.Fatal(err)
	}
	d.SetStatus(first.ID, "running")
	d.SetStatus(first.ID, "timeout")
	tr.Enabled = true
	if err := d.UpdateTrigger(tr); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT COUNT(*) FROM trigger_runs WHERE trigger_id=$1`, tr.ID).Scan(&count); err != nil || count != 2 {
		t.Fatal("disabled events replayed", count, err)
	}
	runs, err := d.ClaimTriggerRuns(t.Context(), 64, nil)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, r := range runs {
		if r.AgentKey == key {
			ids = append(ids, r.ID)
		}
	}
	if len(ids) != 2 {
		t.Fatal("pending events missing", ids)
	}
	c, err := d.StartTriggerConversation(t.Context(), ids, key, "trigger", "inspect")
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteConversation(c.ID)
	if _, err := d.StartTriggerConversation(t.Context(), ids, key, "duplicate", "inspect"); err == nil {
		t.Fatal("duplicate conversation created")
	}
	if err := d.RecoverTriggerRuns(); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := d.QueryRow(`SELECT state FROM trigger_runs WHERE id=$1`, ids[0]).Scan(&state); err != nil || state != "interrupted" {
		t.Fatal(state, err)
	}
}
