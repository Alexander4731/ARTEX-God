package db

import (
	"context"
	"testing"
)

func reliabilityDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestBootstrapAndCompletionAreAtomic(t *testing.T) {
	d := reliabilityDB(t)
	task, err := d.CreateTask("bootstrap", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.DeleteTask(task.ID) })
	s := d.Exploration(task.ExplorationID)
	if won, err := d.SetTerminalStatusGuarded(task.ID, "done"); err != nil || won {
		t.Fatalf("empty bootstrap completed: %v %v", won, err)
	}
	if _, err := s.CompleteBootstrap([]map[string]any{{"text": "one"}, {"text": make(chan int)}}); err == nil {
		t.Fatal("invalid goal accepted")
	}
	counts, err := s.NodeCounts()
	if err != nil || counts[KindGoal] != 0 {
		t.Fatalf("partial bootstrap committed: %v %v", counts, err)
	}
	ids, err := s.CompleteBootstrap([]map[string]any{{"text": "one"}, {"text": "two"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteBootstrap([]map[string]any{{"text": "duplicate"}}); err != nil {
		t.Fatal(err)
	}
	counts, _ = s.NodeCounts()
	if counts[KindGoal] != 2 {
		t.Fatalf("duplicate bootstrap goals: %v", counts)
	}
	evidence, err := s.AddNode(KindFact, map[string]any{"summary": "evidence"}, 0, "confirmed", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ProveGoal(ids[0], evidence); err != nil {
		t.Fatal(err)
	}
	if won, err := d.SetTerminalStatusGuarded(task.ID, "done"); err != nil || won {
		t.Fatalf("unmet goal accepted: %v %v", won, err)
	}
	if err := s.ProveGoal(ids[1], evidence); err != nil {
		t.Fatal(err)
	}
	if won, err := d.SetTerminalStatusGuarded(task.ID, "done"); err != nil || !won {
		t.Fatalf("completed goals rejected: %v %v", won, err)
	}
	if _, err := s.AddIntent(map[string]any{"summary": "late"}, 0, nil, ""); err == nil {
		t.Fatal("late intent added to completed task")
	}
}

func TestRunningIntentsAndTodosSurviveLongHistory(t *testing.T) {
	d := reliabilityDB(t)
	id, err := d.CreateExploration("long history", "goal")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM explorations WHERE id=$1`, id) })
	s := d.Exploration(id)
	old, err := s.AddIntent(map[string]any{"summary": "old active"}, 0, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ClaimIntent(old, "worker"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, err := d.Exec(`INSERT INTO exploration_nodes(exploration_id,kind,payload,state) SELECT $1,'intent','{}','done' FROM generate_series(1,400)`, id); err != nil {
		t.Fatal(err)
	}
	running, err := s.IntentsInState("running")
	if err != nil || len(running) != 1 || running[0].ID != old {
		t.Fatalf("lost running intent: %v %v", running, err)
	}
	if err := s.SavePlannerTodos([]byte(`[{"content":"resume","status":"pending","activeForm":"resuming"}]`), 0); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePlannerTodos([]byte(`[]`), 0); err == nil {
		t.Fatal("stale TODO revision overwrote state")
	}
	_, revision, err := d.Exploration(id).PlannerTodos()
	if err != nil || revision != 1 {
		t.Fatal(revision, err)
	}
}

func TestRuntimeLockExcludesSecondExecutor(t *testing.T) {
	d := reliabilityDB(t)
	first, err := d.AcquireRuntimeLock(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if second, err := d.AcquireRuntimeLock(context.Background(), nil); err == nil {
		second.Close()
		t.Fatal("second executor acquired lock")
	}
}
