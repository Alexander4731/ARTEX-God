package server

import (
	"encoding/json"
	pgdb "github.com/Autumn-27/artex/db"
	"os"
	"path/filepath"
	"testing"
)

func TestTaskArchiveExecutionSpoolRoundTrip(t *testing.T) {
	root := t.TempDir()
	own := filepath.Join(root, "transcripts", "execution-receipts", "12", "process-output.txt")
	other := filepath.Join(root, "transcripts", "execution-receipts", "13", "process-output.txt")
	for _, name := range []string{own, other} {
		if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte("原文"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	stage, err := stageTaskArchiveFiles(root, 1, "42", 7, 12)
	if err != nil {
		t.Fatal(err)
	}
	s := &pgdb.TaskArchiveSnapshot{FormatVersion: 4, TaskID: 42, Tables: map[string]json.RawMessage{"tool_execution_receipts": json.RawMessage(`[{"approval_id":12,"task_id":"42","run_id":"run","tool_use_id":"call"}]`)}}
	if err = archiveExecutionFiles(stage.payload, s, false); err != nil {
		t.Fatal(err)
	}
	if len(s.ExecutionFiles) != 1 || s.ExecutionFiles[0].RunID != "run" {
		t.Fatalf("manifest: %+v", s.ExecutionFiles)
	}
	if _, err = os.Stat(other); err != nil {
		t.Fatal("other task spool moved", err)
	}
	if err = archiveExecutionFiles(stage.payload, s, true); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(stage.payload, "executions", "12", "process-output.txt")
	if err = os.WriteFile(name, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = archiveExecutionFiles(stage.payload, s, true); err == nil {
		t.Fatal("accepted changed spool")
	}
	if err = os.WriteFile(name, []byte("原文"), 0600); err != nil {
		t.Fatal(err)
	}
	installed, err := installTaskArchiveFiles(root, stage.payload, "42", 1)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(own); err != nil || string(b) != "原文" {
		t.Fatalf("restored output: %q %v", b, err)
	}
	if err = installed.rollback(); err != nil {
		t.Fatal(err)
	}
	if err = stage.rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(own); err != nil {
		t.Fatal(err)
	}
}
