package tool

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProcessReceiptPrecedesOutputCleanup(t *testing.T) {
	m := newTestManager(t)
	var mu sync.Mutex
	var events []ExecutionEvent
	var saved string
	ctx := WithExecutionToolName(WithExecutionCallID(context.Background(), "receipt-call"), "Bash")
	ctx = WithExecutionObserver(ctx, func(_ context.Context, e ExecutionEvent) error {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, e)
		if e.Kind == "process_exited" {
			raw, err := os.ReadFile(e.OutputPath)
			if err != nil {
				t.Error(err)
			}
			saved = string(raw)
		}
		return nil
	})
	task, finished, _, err := m.RunForeground(ctx, SpawnSpec{Command: "echo receipt-fixture", ExecutionContext: ctx}, 5*time.Second)
	if err != nil || !finished {
		t.Fatal(finished, err)
	}
	m.Forget(task.ID)
	mu.Lock()
	defer mu.Unlock()
	if len(events) != 2 || events[0].Kind != "process_started" || events[1].Kind != "process_exited" {
		t.Fatal(events)
	}
	if events[1].ToolUseID != "receipt-call" || events[1].ExitCode == nil || *events[1].ExitCode != 0 || !strings.Contains(saved, "receipt-fixture") {
		t.Fatal(events, saved)
	}
	if _, err := os.Stat(task.OutputPath); !os.IsNotExist(err) {
		t.Fatal("foreground output was not cleaned up after receipt")
	}
}
