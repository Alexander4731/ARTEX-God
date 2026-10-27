package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

func (s *ExplorationStore) BootstrapReady() (bool, error) {
	var ready bool
	err := s.db.QueryRow(`SELECT bootstrap_complete FROM tasks WHERE exploration_id=$1`, s.expID).Scan(&ready)
	if err == sql.ErrNoRows {
		return true, nil
	}
	return ready, err
}

// CompleteBootstrap commits the initial goal batch and its ready marker under
// the task lock. Repeating a submitted batch after a crash is a no-op.
func (s *ExplorationStore) CompleteBootstrap(goals []map[string]any) ([]int64, error) {
	if len(goals) == 0 {
		return nil, fmt.Errorf("初始化目标不能为空")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var ready bool
	err = tx.QueryRow(`SELECT bootstrap_complete FROM tasks WHERE exploration_id=$1 FOR UPDATE`, s.expID).Scan(&ready)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if ready {
		return nil, nil
	}
	var root int64
	err = tx.QueryRow(`SELECT id FROM exploration_nodes WHERE exploration_id=$1 AND kind='fact' AND state='origin' ORDER BY id LIMIT 1`, s.expID).Scan(&root)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	ids := make([]int64, 0, len(goals))
	for _, goal := range goals {
		raw, err := json.Marshal(goal)
		if err != nil {
			return nil, err
		}
		var id int64
		if err := tx.QueryRow(`INSERT INTO exploration_nodes(exploration_id,kind,payload,priority,state,origin)
VALUES($1,'goal',$2,0,'open','goals') RETURNING id`, s.expID, raw).Scan(&id); err != nil {
			return nil, err
		}
		if root > 0 {
			if _, err := tx.Exec(`INSERT INTO exploration_edges(exploration_id,src_id,rel,dst_id) VALUES($1,$2,$3,$4)`, s.expID, root, RelSpawns, id); err != nil {
				return nil, err
			}
		}
		ids = append(ids, id)
	}
	if _, err := tx.Exec(`UPDATE tasks SET bootstrap_complete=TRUE WHERE exploration_id=$1`, s.expID); err != nil {
		return nil, err
	}
	return ids, tx.Commit()
}
