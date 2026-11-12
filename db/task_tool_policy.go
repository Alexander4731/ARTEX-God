package db

import "context"

// TaskToolPolicy defaults to the global tool policy unless explicitly enabled.
type TaskToolPolicy struct {
	AllowAll bool `json:"allow_all"`
}

func (d *DB) GetTaskToolPolicy(ctx context.Context, taskID int64) (TaskToolPolicy, error) {
	var policy TaskToolPolicy
	err := d.QueryRowContext(ctx, `SELECT COALESCE((SELECT allow_all FROM task_tool_policies WHERE task_id=$1), false)`, taskID).Scan(&policy.AllowAll)
	return policy, err
}

func (d *DB) SetTaskToolPolicy(ctx context.Context, taskID int64, policy TaskToolPolicy) error {
	_, err := d.ExecContext(ctx, `INSERT INTO task_tool_policies(task_id, allow_all) VALUES ($1,$2)
ON CONFLICT (task_id) DO UPDATE SET allow_all=EXCLUDED.allow_all`, taskID, policy.AllowAll)
	return err
}
