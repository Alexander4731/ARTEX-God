package intercept

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/tool"
)

func withSDKExecutionCall(ctx context.Context) context.Context {
	return WithExecutionCall(ctx, tool.ExecutionCallID(ctx))
}

// TrustedReadTool is deliberately narrower than arbitrary tool self-reporting.
// Bash/Python, HTTP tools and user-defined tools are never replay-safe by name.
func TrustedReadTool(name string) bool {
	switch name {
	case "Read", "Glob", "Grep", "LS", "TaskOutput", "TaskList":
		return true
	}
	return false
}

func (i *Interceptor) CheckExecutionRecovery(ctx context.Context, name string) error {
	switch name {
	case "Bash", "Write", "Edit", "MultiEdit", "shell_open", "shell_send":
	default:
		return nil
	}
	taskID, _ := taskInfoFromCtx(ctx)
	if taskID == "" {
		return nil
	}
	blocked, err := i.db.HasUncertainWrites(ctx, taskID)
	if err != nil {
		return err
	}
	if blocked {
		return fmt.Errorf("有可能尚未完成的写操作正在核验。可读取已有输出或处理无冲突工作；不要重发可能已提交的操作。请查看执行恢复列表")
	}
	return nil
}

func (t *Trace) observeExecution(ctx context.Context, event tool.ExecutionEvent) error {
	t.mu.Lock()
	var observers []tool.ExecutionObserver
	if c := t.calls[event.ToolUseID]; c != nil {
		observers = append(observers, c.receipts...)
	}
	t.mu.Unlock()
	for _, observer := range observers {
		if err := observer(ctx, event); err != nil {
			return err
		}
	}
	if event.Kind == "prepared" && len(observers) > 0 {
		t.mu.Lock()
		if c := t.calls[event.ToolUseID]; c != nil {
			c.executorOwned = true
		}
		t.mu.Unlock()
	}
	return nil
}

type spooledReceipt struct {
	ID        int64               `json:"id"`
	RunID     string              `json:"run_id"`
	CallID    string              `json:"call_id"`
	Event     tool.ExecutionEvent `json:"event"`
	Output    string              `json:"output"`
	Truncated bool                `json:"truncated"`
}

func (t *Trace) bindExecution(d *db.DB, id int64, a db.InterceptAudit, toolName string) {
	dir := ""
	if t.store != nil {
		dir = filepath.Join(filepath.Dir(t.store.MainPath("_")), "execution-receipts", strconv.FormatInt(id, 10))
	}
	observer := func(ctx context.Context, event tool.ExecutionEvent) error {
		if event.ToolName != toolName {
			return nil
		}
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if event.Kind == "prepared" {
			return d.PrepareToolExecution(writeCtx, id, a.RunID, a.ToolUseID, event.WorkingDir, TrustedReadTool(event.ToolName) && event.ReadOnly)
		}
		output := event.Output
		if event.Kind == "process_exited" && event.OutputPath != "" {
			// Snapshot before norma's Forget/Session.Close can remove the source.
			if dir != "" {
				if err := os.MkdirAll(dir, 0700); err != nil {
					log.Printf("[execution] output directory %d unavailable, retaining database receipt: %v", id, err)
				} else {
					destination := filepath.Join(dir, "process-output.txt")
					stored, total, err := snapshotExecutionOutput(event.OutputPath, destination, 51<<20)
					complete := err == nil && stored == total
					event.OutputBytes, event.OutputStoredBytes, event.OutputComplete = total, stored, &complete
					if err != nil {
						log.Printf("[execution] output snapshot %d: %v", id, err)
					} else {
						event.OutputPath = destination
					}
				}
			}
			if f, err := os.Open(event.OutputPath); err == nil {
				raw, _ := io.ReadAll(io.LimitReader(f, 512*1024+1))
				f.Close()
				output = string(raw)
			}
		}
		output, cut := bounded(output, 512*1024)
		// Output is stored separately; the envelope does not duplicate large text.
		event.Output = ""
		receipt := spooledReceipt{ID: id, RunID: a.RunID, CallID: a.ToolUseID, Event: event, Output: output, Truncated: cut}
		var pending string
		if dir != "" {
			raw, _ := json.Marshal(receipt)
			pending = filepath.Join(dir, fmt.Sprintf("%x.pending.json", sha256.Sum256(raw)))
			if err := writeReceiptFile(pending, raw); err != nil {
				log.Printf("[execution] receipt spool %d unavailable, trying database: %v", id, err)
				pending = ""
			}
		}
		raw, _ := json.Marshal(event)
		if err := d.StoreExecutionEvent(writeCtx, id, event.Kind, event.Status, output, cut, raw); err != nil {
			log.Printf("[execution] receipt %d queued for replay: %v", id, err)
			return err
		}
		if pending != "" {
			_ = os.Remove(pending)
		}
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if c := t.calls[a.ToolUseID]; c != nil {
		c.receipts = append(c.receipts, observer)
	}
}

func writeReceiptFile(path string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "receipt-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func snapshotExecutionOutput(src, dst string, limit int64) (stored, total int64, err error) {
	f, err := os.Open(src)
	if err != nil {
		return stored, total, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return 0, 0, err
	}
	total = info.Size()
	out, err := os.CreateTemp(filepath.Dir(dst), "output-*.tmp")
	if err != nil {
		return stored, total, err
	}
	defer os.Remove(out.Name())
	stored, err = io.Copy(out, io.LimitReader(f, limit))
	if err != nil {
		out.Close()
		return stored, total, err
	}
	if err = out.Sync(); err != nil {
		out.Close()
		return stored, total, err
	}
	if err = out.Close(); err != nil {
		return stored, total, err
	}
	err = os.Rename(out.Name(), dst)
	return stored, total, err
}

// ReplayExecutionReceipts only reads host-owned receipt envelopes and requires
// exact original execution identity. It never invokes the underlying tool.
func ReplayExecutionReceipts(ctx context.Context, d *db.DB, transcriptDir string, taskIDs ...string) error {
	taskID := ""
	if len(taskIDs) > 0 {
		taskID = taskIDs[0]
	}
	paths, err := filepath.Glob(filepath.Join(transcriptDir, "execution-receipts", "*", "*.pending.json"))
	if err != nil {
		return err
	}
	for _, path := range paths {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		raw, err := io.ReadAll(io.LimitReader(f, 2<<20))
		f.Close()
		if err != nil {
			continue
		}
		var r spooledReceipt
		if json.Unmarshal(raw, &r) != nil {
			continue
		}
		var matches bool
		if err = d.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tool_execution_receipts WHERE approval_id=$1 AND run_id=$2 AND tool_use_id=$3 AND ($4='' OR task_id=$4))`, r.ID, r.RunID, r.CallID, taskID).Scan(&matches); err != nil {
			return err
		}
		if !matches {
			continue
		}
		payload, _ := json.Marshal(r.Event)
		if err = d.StoreExecutionEvent(ctx, r.ID, r.Event.Kind, r.Event.Status, r.Output, r.Truncated, payload); err != nil {
			return err
		}
		_ = os.Remove(path)
	}
	return nil
}

type recoverySummaryKey struct{}

func WithRecoverySummary(ctx context.Context, summary string) context.Context {
	return context.WithValue(ctx, recoverySummaryKey{}, summary)
}
func RecoverySummary(ctx context.Context) string {
	s, _ := ctx.Value(recoverySummaryKey{}).(string)
	return s
}
