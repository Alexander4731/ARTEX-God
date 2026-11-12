package intercept

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/tool"
	"github.com/Autumn-27/norma/transcript"
)

func TestReceiptDirectoryFailureStillPersistsProcessExit(t *testing.T) {
	dsn, _, err := db.DSN()
	if err != nil {
		t.Skip("test database not configured")
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	task, err := d.CreateTask(t.Name(), "fixture", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	ctx, trace := WithTrace(WithTaskContext(t.Context(), fmt.Sprint(task.ID), "worker", nil), "fixture", nil)
	dir := t.TempDir()
	trace.UseTranscript(transcript.NewStore(dir))
	trace.Start("call", "Bash", []byte(`{}`))
	ic := New(d)
	if err := ic.LogChecked(WithCall(ctx, "Bash", []byte(`{}`)), 0, Decision{Action: "allow"}, "Bash", []byte(`{}`), "allowed"); err != nil {
		t.Fatal(err)
	}
	if err := tool.ObserveExecution(ctx, tool.ExecutionEvent{ToolUseID: "call", ToolName: "Bash", Kind: "prepared"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "execution-receipts"), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "output.txt")
	if err := os.WriteFile(output, []byte("preserved partial output"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := tool.ObserveExecution(ctx, tool.ExecutionEvent{ToolUseID: "call", ToolName: "Bash", Kind: "process_exited", Status: "failed", OutputPath: output}); err != nil {
		t.Fatal("filesystem failure discarded database receipt", err)
	}
	rows, err := d.ListTaskIntercepts(fmt.Sprint(task.ID))
	if err != nil || len(rows) != 1 {
		t.Fatal(err)
	}
	e, err := d.GetExecution(t.Context(), fmt.Sprint(task.ID), rows[0].ID)
	if err != nil || e.ExecutionState != "failed" || e.Output != "preserved partial output" {
		t.Fatal(e, err)
	}
}
