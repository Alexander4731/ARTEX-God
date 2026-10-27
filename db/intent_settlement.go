package db

import (
	"context"
	"fmt"
)

// SettleIntent retries safely after an uncertain commit without replaying work.
func (s *ExplorationStore) SettleIntent(ctx context.Context, id int64, state, reason string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE exploration_nodes
SET state=$3, blocked_reason=NULLIF($4,''), content_version=content_version+1,
completed_at=CASE WHEN $3 IN ('done','blocked','exhausted','stopped') THEN COALESCE(completed_at,now()) ELSE NULL END
WHERE exploration_id=$1 AND id=$2 AND kind='intent' AND state='running'`, s.expID, id, state, reason)
	if err != nil {
		return err
	}
	changed, err := res.RowsAffected()
	if err != nil || changed > 0 {
		return err
	}
	var current string
	if err := s.db.QueryRowContext(ctx, `SELECT state FROM exploration_nodes WHERE exploration_id=$1 AND id=$2 AND kind='intent'`, s.expID, id).Scan(&current); err != nil {
		return err
	}
	if current != state {
		return fmt.Errorf("%w: intent %d is %s", ErrIntentStateConflict, id, current)
	}
	return nil
}
