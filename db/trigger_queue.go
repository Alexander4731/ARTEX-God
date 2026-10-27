package db

import (
	"context"
	_ "embed"
	"fmt"
)

//go:embed trigger_queue.sql
var triggerQueueSchema string

type TriggerRun struct {
	ID                        int64
	AgentKey, Title, Message  string
	TaskID                    int64
	TaskDescription, TaskGoal string
}

func (d *DB) RecoverTriggerRuns() error {
	_, err := d.Exec(`UPDATE trigger_runs SET state=CASE WHEN state='queued' THEN 'pending' ELSE 'interrupted' END,
error=CASE WHEN state='running' THEN '服务中断，请在原会话核验执行结果' ELSE error END
WHERE state IN ('queued','running')`)
	return err
}

func (d *DB) RecoverUnloadedTriggers(ctx context.Context, live []int64) error {
	if live == nil {
		live = []int64{}
	}
	_, err := d.ExecContext(ctx, `UPDATE trigger_runs SET state=CASE WHEN state='queued' THEN 'pending' ELSE 'interrupted' END,
error=CASE WHEN state='running' THEN '执行交接中断，请在原会话核验' ELSE error END
WHERE state IN ('queued','running') AND NOT(id=ANY($1))`, live)
	return err
}

func (d *DB) EnqueueDueTriggers(ctx context.Context) error {
	_, err := d.ExecContext(ctx, `WITH due AS (
SELECT id FROM agent_triggers WHERE enabled AND interval_sec>0 AND
(last_fire IS NULL OR last_fire+make_interval(secs=>interval_sec)<=now()) FOR UPDATE SKIP LOCKED),
fired AS (UPDATE agent_triggers SET last_fire=now() WHERE id IN (SELECT id FROM due) RETURNING *)
INSERT INTO trigger_runs(trigger_id,agent_key,event_kind,title,message)
SELECT id,agent_key,'interval','定时触发',interval_message || E'\n\n【定时触发】' || last_fire::text FROM fired`)
	return err
}

func (d *DB) ClaimTriggerRuns(ctx context.Context, limit int, excluded []string) ([]TriggerRun, error) {
	if _, err := d.ExecContext(ctx, `UPDATE trigger_runs SET state='failed',error='触发消息超过 256 KiB，请缩短配置后重新触发',finished_at=now() WHERE state='pending' AND octet_length(message)>262144`); err != nil {
		return nil, err
	}
	if excluded == nil {
		excluded = []string{}
	}
	rows, err := d.QueryContext(ctx, `WITH ready AS (
SELECT id FROM trigger_runs WHERE state='pending' AND NOT(agent_key=ANY($2)) ORDER BY id LIMIT $1 FOR UPDATE SKIP LOCKED)
UPDATE trigger_runs q SET state='queued' FROM ready WHERE q.id=ready.id
RETURNING q.id,q.agent_key,q.title,q.message,q.task_id,q.task_description,q.task_goal`, limit, excluded)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []TriggerRun
	for rows.Next() {
		var r TriggerRun
		if err := rows.Scan(&r.ID, &r.AgentKey, &r.Title, &r.Message, &r.TaskID, &r.TaskDescription, &r.TaskGoal); err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

func (d *DB) EnqueueTriggerRun(ctx context.Context, r TriggerRun) error {
	_, err := d.ExecContext(ctx, `INSERT INTO trigger_runs(agent_key,event_kind,task_id,title,message,task_description,task_goal) VALUES($1,'manual',$2,$3,$4,$5,$6)`, r.AgentKey, r.TaskID, r.Title, r.Message, r.TaskDescription, r.TaskGoal)
	return err
}

func (d *DB) ReleaseTriggerRuns(ctx context.Context, ids []int64) error {
	_, err := d.ExecContext(ctx, `UPDATE trigger_runs SET state='pending' WHERE id=ANY($1) AND state='queued'`, ids)
	return err
}

// Conversation creation and ownership of every merged event commit together.
func (d *DB) StartTriggerConversation(ctx context.Context, ids []int64, agentKey, title, message string) (*Conversation, error) {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id,state FROM trigger_runs WHERE id=ANY($1) ORDER BY id FOR UPDATE`, ids)
	if err != nil {
		return nil, err
	}
	n := 0
	for rows.Next() {
		var id int64
		var state string
		if err := rows.Scan(&id, &state); err != nil {
			rows.Close()
			return nil, err
		}
		if state != "queued" {
			rows.Close()
			return nil, fmt.Errorf("触发事件 %d 已处理", id)
		}
		n++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if n != len(ids) || n == 0 {
		return nil, fmt.Errorf("触发事件不存在")
	}
	var cid int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO conversations(agent_key,title) VALUES($1,$2) RETURNING id`, agentKey, title).Scan(&cid); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO conversation_activities(conversation_id,worker,kind,summary,detail) VALUES($1,$2,'user',$3,$4)`, cid, agentKey, title, message); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE trigger_runs SET state='running',conversation_id=$2 WHERE id=ANY($1)`, ids, cid); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &Conversation{ID: cid, AgentKey: agentKey, Title: title}, nil
}

func (d *DB) FinishTriggerRuns(ctx context.Context, ids []int64, status, reason string) error {
	_, err := d.ExecContext(ctx, `UPDATE trigger_runs SET state=$2,error=$3,finished_at=now() WHERE id=ANY($1) AND state='running'`, ids, status, reason)
	return err
}
