package server

import (
	"testing"
)

func TestCreatedTaskAdmissionPersistsRunning(t *testing.T) {
	for _, route := range []string{"direct", "already_started", "fifo"} {
		t.Run(route, func(t *testing.T) {
			m, err := NewManager(t.TempDir(), "")
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			defer restoreConcurrencySetting(t, m)()
			if err := m.SetConcurrency(false, defaultConcurrencyLimit); err != nil {
				t.Fatal(err)
			}
			task, err := m.CreateTask("created admission", "test lifecycle", nil, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer m.DeleteTask(task.ID, DeleteTaskOptions{})
			s := newAdmissionTestServer(m, nil)
			switch route {
			case "already_started":
				s.engine.started.Store(task.ID, true)
			case "fifo":
				if err := m.SetConcurrency(true, 1); err != nil {
					t.Fatal(err)
				}
				unavailable := newAdmissionTestServer(m, func(*Task) bool { return false })
				queued, err := unavailable.admitTask(task, "bootstrap")
				if err != nil || !queued {
					t.Fatalf("queue: %v %v", queued, err)
				}
				if task.lifecycleSnapshot().Status != "created" {
					t.Fatal("never-admitted task changed status while queued")
				}
				s.reconcileConcurrency()
			}
			if route != "fifo" {
				if queued, err := s.admitTask(task, "bootstrap"); err != nil || queued {
					t.Fatalf("admit: %v %v", queued, err)
				}
			}
			stored, err := m.pg.GetTask(mustTaskID(t, task.ID))
			if err != nil {
				t.Fatal(err)
			}
			if stored.Status != "running" || task.lifecycleSnapshot().Status != "running" || stored.Queued {
				t.Fatalf("admitted task did not persist running: DB=%+v memory=%+v", stored, task.lifecycleSnapshot())
			}
		})
	}
}

func TestPausedTaskAdmissionPreservesClock(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	defer restoreConcurrencySetting(t, m)()
	if err := m.SetConcurrency(false, defaultConcurrencyLimit); err != nil {
		t.Fatal(err)
	}
	task, err := m.CreateTask("paused admission", "keep original budget", nil, 120, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer m.DeleteTask(task.ID, DeleteTaskOptions{})
	if _, err := m.StampTaskFirstRun(task.ID); err != nil {
		t.Fatal(err)
	}
	before := task.lifecycleSnapshot()
	if err := m.ApplyTaskPause(task.ID); err != nil {
		t.Fatal(err)
	}
	s := newAdmissionTestServer(m, nil)
	if _, err := s.admitPausedTask(task); err != nil {
		t.Fatal(err)
	}
	stored, err := m.pg.GetTask(mustTaskID(t, task.ID))
	if err != nil {
		t.Fatal(err)
	}
	if stored.FirstRunAt == nil || stored.DeadlineAt == nil || stored.FirstRunAt.Unix() != before.FirstRunAt || stored.DeadlineAt.Unix() != before.DeadlineAt {
		t.Fatalf("ordinary resume reset the budget: %+v", stored)
	}
}

func TestTerminalTaskImmediateAdmissionGetsFreshBudget(t *testing.T) {
	for _, status := range []string{"done", "failed", "timeout"} {
		t.Run(status, func(t *testing.T) {
			m, err := NewManager(t.TempDir(), "")
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			defer restoreConcurrencySetting(t, m)()
			if err := m.SetConcurrency(false, defaultConcurrencyLimit); err != nil {
				t.Fatal(err)
			}
			task, err := m.CreateTask("terminal immediate", "fresh budget", nil, 120, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer m.DeleteTask(task.ID, DeleteTaskOptions{})
			if _, err := m.StampTaskFirstRun(task.ID); err != nil {
				t.Fatal(err)
			}
			if err := m.SetTaskStatus(task.ID, status); err != nil {
				t.Fatal(err)
			}
			s := newAdmissionTestServer(m, nil)
			s.engine.settling.Store(task.ID, true)
			s.engine.stamped.Store(task.ID, true)
			s.engine.deadline.Store(task.ID, int64(1))
			if queued, err := s.admitTask(task, "resume"); err != nil || queued {
				t.Fatalf("admission: %v %v", queued, err)
			}
			state := task.lifecycleSnapshot()
			if state.FirstRunAt != 0 || state.DeadlineAt != 0 || s.engine.isSettling(task.ID) {
				t.Fatalf("stale terminal clock: %+v", state)
			}
			if _, err := m.StampTaskFirstRun(task.ID); err != nil {
				t.Fatal(err)
			}
			stored, err := m.pg.GetTask(mustTaskID(t, task.ID))
			if err != nil {
				t.Fatal(err)
			}
			if stored.Status != "running" || stored.CompletedAt != nil || stored.FirstRunAt == nil || stored.DeadlineAt == nil || stored.DeadlineAt.Sub(*stored.FirstRunAt).Seconds() != 120 {
				t.Fatalf("fresh budget missing: %+v", stored)
			}
		})
	}
}
