package tool

import (
	"context"
	"encoding/json"
	"time"
)

// ExecutionEvent is executor evidence, never a synthetic model-stream result.
type ExecutionEvent struct {
	Kind              string          `json:"kind"`
	ToolUseID         string          `json:"tool_use_id"`
	ToolName          string          `json:"tool_name,omitempty"`
	Input             json.RawMessage `json:"input,omitempty"`
	WorkingDir        string          `json:"working_dir,omitempty"`
	ReadOnly          bool            `json:"read_only,omitempty"`
	Status            string          `json:"status,omitempty"`
	Output            string          `json:"output,omitempty"`
	OutputPath        string          `json:"output_path,omitempty"`
	OutputBytes       int64           `json:"output_bytes,omitempty"`
	OutputStoredBytes int64           `json:"output_stored_bytes,omitempty"`
	OutputComplete    *bool           `json:"output_complete,omitempty"` // nil = legacy/unknown
	ProcessID         int             `json:"process_id,omitempty"`
	ProcessStartedAt  time.Time       `json:"process_started_at,omitempty"`
	ExitCode          *int            `json:"exit_code,omitempty"`
	CancelCause       string          `json:"cancel_cause,omitempty"`
	At                time.Time       `json:"at"`
}

type ExecutionObserver func(context.Context, ExecutionEvent) error
type executionObserverKey struct{}
type executionCallKey struct{}
type executionToolNameKey struct{}

func WithExecutionToolName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, executionToolNameKey{}, name)
}

func WithExecutionObserver(ctx context.Context, observer ExecutionObserver) context.Context {
	return context.WithValue(ctx, executionObserverKey{}, observer)
}

func WithExecutionCallID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, executionCallKey{}, id)
}

func ExecutionCallID(ctx context.Context) string {
	id, _ := ctx.Value(executionCallKey{}).(string)
	return id
}

func ObserveExecution(ctx context.Context, event ExecutionEvent) error {
	if ctx == nil {
		return nil
	}
	observer, _ := ctx.Value(executionObserverKey{}).(ExecutionObserver)
	if observer == nil {
		return nil
	}
	if event.ToolUseID == "" {
		event.ToolUseID = ExecutionCallID(ctx)
	}
	if event.ToolName == "" {
		event.ToolName, _ = ctx.Value(executionToolNameKey{}).(string)
	}
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	return observer(ctx, event)
}
