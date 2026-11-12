package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/guard"
	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
	"github.com/Autumn-27/norma/transcript"
)

func TestLateExecutorResultSurvivesCanceledModelStream(t *testing.T) {
	dsn, _, err := db.DSN()
	if err != nil {
		t.Skip("test database not configured")
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	task, err := d.CreateTask("late receipt fixture", "fixture", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	if err = d.SetTaskToolPolicy(t.Context(), task.ID, db.TaskToolPolicy{AllowAll: true}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(intercept.WithTaskContext(t.Context(), fmt.Sprint(task.ID), "fixture", nil))
	defer cancel()
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	probe := tool.Build(tool.Spec{Name: "late_fixture", Schema: map[string]any{"type": "object"}, Run: func(ctx context.Context, _ json.RawMessage, _ *tool.ToolContext) (tool.Result, error) {
		close(started)
		<-ctx.Done()
		<-release
		return tool.Errorf("verified partial fixture output"), nil
	}})
	provider := captureUsageProvider{stream: func(_ context.Context, yield func(llm.StreamEvent, error) bool) {
		for _, e := range []llm.StreamEvent{{Type: llm.SEToolUseStart, ToolID: "late-call", ToolName: "late_fixture"}, {Type: llm.SEToolInputJSON, Text: `{}`}, {Type: llm.SEMessageDelta, StopReason: "tool_use"}} {
			if !yield(e, nil) {
				return
			}
		}
	}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		captureRun(ctx, agentcore.Options{Provider: provider, Tools: []tool.CoreTool{probe}, Hooks: guard.NewWithInterceptor(intercept.New(d)).Hooks(), Transcript: transcript.NewStore(t.TempDir()), SessionID: "late-receipt", WorkingDir: t.TempDir(), PermissionMode: permission.ModeBypass}, "fixture", nil)
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("tool did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("model stream did not close")
	}
	rows, err := d.ListTaskIntercepts(fmt.Sprint(task.ID))
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	detail, _ := d.GetInterceptDetail(rows[0].ID)
	if detail.Audit.ExecutionStatus != "unknown" {
		t.Fatal("synthetic interruption fabricated a result")
	}
	once.Do(func() { close(release) })
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		detail, err = d.GetInterceptDetail(rows[0].ID)
		if err == nil && detail.Audit.ExecutionStatus == "failed" && detail.Audit.Output == "verified partial fixture output" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("late result missing: %+v %v", detail, err)
}
