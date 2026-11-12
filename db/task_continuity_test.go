package db

import "testing"

func TestPausedIntentCannotCompleteGoallessTask(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	task, err := d.CreateTask(t.Name(), "fixture", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	store := d.Exploration(task.ExplorationID)
	id, err := store.AddIntent(map[string]any{"summary": "awaiting recovery"}, 5, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SetNodeState(id, "paused"); err != nil {
		t.Fatal(err)
	}
	if _, err = d.Exec(`UPDATE tasks SET bootstrap_complete=true WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	if pending, err := store.HasPendingIntent(); err != nil || !pending {
		t.Fatalf("pending=%v err=%v", pending, err)
	}
	if won, err := d.SetTerminalStatusGuarded(task.ID, "done"); err != nil || won {
		t.Fatalf("premature completion won=%v err=%v", won, err)
	}
	if err = store.SetNodeState(id, "done"); err != nil {
		t.Fatal(err)
	}
	if won, err := d.SetTerminalStatusGuarded(task.ID, "done"); err != nil || !won {
		t.Fatalf("completed work won=%v err=%v", won, err)
	}
}
