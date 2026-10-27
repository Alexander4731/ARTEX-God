package db

import (
	"encoding/json"
	"fmt"
)

func (s *ExplorationStore) PlannerTodos() (json.RawMessage, int64, error) {
	var items json.RawMessage
	var revision int64
	err := s.db.QueryRow(`SELECT planner_todos,planner_todo_revision FROM explorations WHERE id=$1`, s.expID).Scan(&items, &revision)
	return items, revision, err
}

func (s *ExplorationStore) SavePlannerTodos(items json.RawMessage, revision int64) error {
	result, err := s.db.Exec(`UPDATE explorations SET planner_todos=$2,planner_todo_revision=planner_todo_revision+1
WHERE id=$1 AND planner_todo_revision=$3`, s.expID, items, revision)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err == nil && changed != 1 {
		return fmt.Errorf("规划待办已被另一轮修改")
	}
	return err
}
