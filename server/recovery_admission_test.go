package server

import (
	"context"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
)

func TestRecoveredTaskAdmissionStartsRuntimeAfterBootstrap(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	task, err := m.CreateTask("recover admission", "resume saved goal", nil, 60, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer m.DeleteTask(task.ID, DeleteTaskOptions{})
	if _, err := task.Store.CompleteBootstrap([]map[string]any{{"text": task.Goal}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	s := &Server{m: m, engine: NewEngine(m), ctx: ctx, stop: cancel}
	defer func() {
		drain, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		if err := s.Shutdown(drain); err != nil {
			t.Error(err)
		}
	}()
	s.engine.Pause(task.ID, agent.AbortPausedOnReload)
	s.startAdmittedTask(task, "resume")
	if s.engine.IsPaused(task.ID) || !s.engine.Started(task.ID) {
		t.Fatal("recovered task was not admitted")
	}
	if _, ok := s.engine.coordStarted.Load(task.ID); !ok {
		t.Fatal("deadline coordinator was not restarted")
	}
}
