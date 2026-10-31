package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// UnsettledExecution identifies an allowed call whose result needs recovery.
type UnsettledExecution struct {
	ID             int64
	ExplorationID  int64
	ConversationID int64
	AgentName      string
	ToolName       string
	MainSegment    *int
	OriginalCalls  int
	Audit          InterceptAudit
}

// An empty taskID is used only during startup, before any tools can be running.
func (d *DB) UnsettledInterceptExecutions(ctx context.Context, taskID string) ([]UnsettledExecution, error) {
	rows, err := d.QueryContext(ctx, `SELECT ip.id,COALESCE(t.exploration_id,0),
COALESCE(ip.conversation_id,0),ip.agent_name,ip.tool_name,ip.audit,
(SELECT min(COALESCE(a.main_seg,0)) FROM activity a WHERE a.exploration_id=t.exploration_id
 AND COALESCE(ip.audit->>'session_id','')='' AND ip.agent_name='mainagent'
 AND a.worker='mainagent' AND a.kind='tool_use' AND a.tool_use_id=ip.audit->>'tool_use_id'
 HAVING count(*)=1),
CASE WHEN COALESCE(ip.audit->>'session_id','')<>'' THEN 0
WHEN ip.conversation_id IS NOT NULL THEN
 (SELECT count(*) FROM conversation_activities ca WHERE ca.conversation_id=ip.conversation_id
  AND ca.kind='tool_use' AND ca.tool_use_id=ip.audit->>'tool_use_id')
ELSE (SELECT count(*) FROM activity a WHERE a.exploration_id=t.exploration_id
 AND a.kind='tool_use' AND a.tool_use_id=ip.audit->>'tool_use_id') END
FROM intercept_pending ip LEFT JOIN tasks t ON ip.task_id=t.id::text
WHERE ($1='' OR ip.task_id=$1) AND ip.audit->>'effective_action'='allow'
AND ip.audit->>'correlation'='exact' AND ip.audit->>'resolution_source' IS NULL
AND ip.audit->>'execution_status' IN ('unknown','awaiting_result')`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var executions []UnsettledExecution
	for rows.Next() {
		var execution UnsettledExecution
		var raw []byte
		if err := rows.Scan(&execution.ID, &execution.ExplorationID, &execution.ConversationID,
			&execution.AgentName, &execution.ToolName, &raw, &execution.MainSegment, &execution.OriginalCalls); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &execution.Audit); err != nil {
			return nil, err
		}
		executions = append(executions, execution)
	}
	return executions, rows.Err()
}

// RecoverInterceptExecution cannot rewrite a settled or manually verified result.
func (d *DB) RecoverInterceptExecution(ctx context.Context, execution UnsettledExecution, status, output string, truncated bool, endedAt time.Time) error {
	if status != "succeeded" && status != "failed" {
		return fmt.Errorf("invalid recorded execution status %q", status)
	}
	patch, err := json.Marshal(map[string]any{
		"execution_status": status, "output": output, "output_truncated": truncated,
		"execution_ended_at": endedAt, "resolution_source": "transcript",
	})
	if err != nil {
		return err
	}
	_, err = d.ExecContext(ctx, `UPDATE intercept_pending SET audit=audit || $4::jsonb
WHERE id=$1 AND audit->>'run_id'=$2 AND audit->>'tool_use_id'=$3
AND audit->>'effective_action'='allow' AND audit->>'correlation'='exact'
AND audit->>'execution_status' IN ('unknown','awaiting_result')
AND audit->>'resolution_source' IS NULL`, execution.ID, execution.Audit.RunID,
		execution.Audit.ToolUseID, jsonbClean(patch))
	return err
}

// Called after this worker has stopped, so remaining awaiting_result rows can no
// longer represent a live tool in this intent.
func (d *DB) FinishIntentExecutionAudit(taskID string, intentID int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := d.ExecContext(ctx, `UPDATE intercept_pending SET audit=audit || '{"execution_status":"unknown"}'::jsonb
WHERE task_id=$1 AND audit->>'intent_id'=$2 AND audit->>'execution_status'='awaiting_result'`, taskID, fmt.Sprint(intentID))
	return err
}

func (d *DB) HasUnknownTaskExecution(taskID string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var unknown bool
	err := d.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM intercept_pending WHERE task_id=$1 AND audit->>'execution_status'='unknown')`, taskID).Scan(&unknown)
	return unknown, err
}

func (d *DB) ResolveUnknownExecution(ctx context.Context, id int64, status, note string) (bool, error) {
	result, err := d.ExecContext(ctx, `UPDATE intercept_pending SET audit=audit || jsonb_build_object('execution_status',$2::text,'output',$3::text,'execution_ended_at',now(),'resolution_source','human')
WHERE id=$1 AND audit->>'execution_status'='unknown'`, id, status, utf8Clean(note))
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

// RecoverIntercepts is called only while the process holds the runtime lock,
// before accepting decisions or starting any agents.
func (d *DB) RecoverIntercepts() error {
	_, err := d.Exec(`UPDATE intercept_pending SET status='denied', decided_at=now(),
reason='执行已中断，请在恢复后的新调用中重新审批',
audit=COALESCE(audit,'{}'::jsonb) || '{"effective_action":"deny","execution_status":"not_executed","decision_reason":"执行已中断"}'::jsonb
WHERE status='pending'`)
	if err != nil {
		return err
	}
	_, err = d.Exec(`UPDATE intercept_pending SET audit=audit || '{"execution_status":"unknown"}'::jsonb
WHERE audit->>'execution_status'='awaiting_result'`)
	if err != nil {
		return err
	}
	_, err = d.Exec(`UPDATE tasks t SET paused=true WHERE status NOT IN ('done','failed','timeout')
AND EXISTS(SELECT 1 FROM intercept_pending ip WHERE ip.task_id=t.id::text
AND ip.audit->>'execution_status'='unknown')`)
	return err
}
