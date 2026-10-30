package db

import (
	"context"
	"fmt"
	"time"
)

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
