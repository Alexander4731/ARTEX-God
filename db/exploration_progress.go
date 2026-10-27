package db

import (
	"database/sql"
	"fmt"
)

func lockTaskGraphWrite(tx *sql.Tx, expID int64) error {
	var status string
	err := tx.QueryRow(`SELECT status FROM tasks WHERE exploration_id=$1 FOR UPDATE`, expID).Scan(&status)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if IsTerminal(status) {
		return fmt.Errorf("任务已结束，不能修改目标或新建意图")
	}
	return nil
}

func (s *ExplorationStore) AllGoalsMet() (bool, error) {
	var met bool
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM exploration_nodes WHERE exploration_id=$1 AND kind='goal')
AND NOT EXISTS(SELECT 1 FROM exploration_nodes WHERE exploration_id=$1 AND kind='goal' AND state<>'met')`, s.expID).Scan(&met)
	return met, err
}

// GraphProgress tracks persisted reasoning changes, excluding activity heartbeats.
func (s *ExplorationStore) GraphProgress() (string, error) {
	var nodes, versions, edges int64
	err := s.db.QueryRow(`SELECT COUNT(*),COALESCE(SUM(content_version),0),
(SELECT COUNT(*) FROM exploration_edges WHERE exploration_id=$1)
FROM exploration_nodes WHERE exploration_id=$1`, s.expID).Scan(&nodes, &versions, &edges)
	return fmt.Sprintf("%d/%d/%d", nodes, versions, edges), err
}

func (s *ExplorationStore) ProveGoal(goal, evidence int64) error {
	e, err := s.GetNodeWithSources(evidence)
	if err != nil {
		return err
	}
	if e == nil || (e.Kind != KindFact && e.Kind != KindFinding) {
		return fmt.Errorf("证据必须是可见的事实或漏洞")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockTaskGraphWrite(tx, s.expID); err != nil {
		return err
	}
	var id int64
	if err := tx.QueryRow(`SELECT id FROM exploration_nodes WHERE id=$1 AND exploration_id=$2 AND kind='goal' FOR UPDATE`, goal, s.expID).Scan(&id); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO exploration_edges(exploration_id,src_id,rel,dst_id)
VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, s.expID, evidence, RelProves, goal); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE exploration_nodes SET state='met',content_version=content_version+1 WHERE id=$1`, goal); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *ExplorationStore) IntentsInState(state string) ([]*Node, error) {
	rows, err := s.db.Query(`SELECT `+nodeCols+` FROM exploration_nodes WHERE exploration_id=$1 AND kind='intent' AND state=$2 ORDER BY id`, s.expID, state)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNodes(rows)
}

func (s *ExplorationStore) NodeCounts() (map[string]int64, error) {
	rows, err := s.db.Query(`SELECT kind,COUNT(*) FROM exploration_nodes WHERE exploration_id=$1 GROUP BY kind`, s.expID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int64{}
	for rows.Next() {
		var kind string
		var count int64
		if err := rows.Scan(&kind, &count); err != nil {
			return nil, err
		}
		counts[kind] = count
	}
	return counts, rows.Err()
}
