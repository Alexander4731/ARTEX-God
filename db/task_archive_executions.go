package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
)

var archiveExecutionTables = []string{
	"tool_execution_receipts", "tool_execution_events", "execution_recoveries", "execution_recovery_decisions",
	"task_execution_policies", "task_tool_policies",
}

func archiveExecutionQuery(table string) string {
	switch table {
	case "task_execution_policies", "task_tool_policies":
		return "SELECT * FROM " + table + " WHERE task_id=$1 ORDER BY task_id"
	case "tool_execution_receipts", "execution_recoveries":
		return "SELECT x.* FROM " + table + " x JOIN intercept_pending p ON p.id=x.approval_id WHERE p.task_id=$1::text ORDER BY x.approval_id"
	default:
		return "SELECT x.* FROM " + table + " x JOIN intercept_pending p ON p.id=x.approval_id WHERE p.task_id=$1::text ORDER BY x.id"
	}
}

// ValidateArchiveExecutions rejects incomplete v4 ledgers before hot data can be
// compacted or restored. v1-v3 never claimed to include executor-owned evidence.
func ValidateArchiveExecutions(s *TaskArchiveSnapshot) error {
	if s.FormatVersion < 4 {
		return nil
	}
	approvals, err := decodeArchiveRows(s.Tables["intercept_pending"])
	if err != nil {
		return err
	}
	ids := make(map[int64]bool, len(approvals))
	for _, p := range approvals {
		id, ok := jsonInt64(p["id"])
		if !ok || id <= 0 {
			return fmt.Errorf("invalid archived approval")
		}
		ids[id] = true
	}
	for _, table := range archiveExecutionTables {
		raw, ok := s.Tables[table]
		if !ok {
			return fmt.Errorf("%w: missing %s", ErrTaskArchiveFormatMismatch, table)
		}
		rows, err := decodeArchiveRows(raw)
		if err != nil {
			return err
		}
		count, ok := s.DataCounts[table]
		if !ok || int64(len(rows)) != count {
			return fmt.Errorf("archive %s count mismatch", table)
		}
		for _, row := range rows {
			if table == "task_execution_policies" || table == "task_tool_policies" {
				taskID, ok := jsonInt64(row["task_id"])
				if !ok || taskID != s.TaskID {
					return fmt.Errorf("archive policy ownership mismatch")
				}
			} else {
				id, ok := jsonInt64(row["approval_id"])
				if !ok || !ids[id] {
					return fmt.Errorf("archive execution ownership mismatch")
				}
				if table == "tool_execution_receipts" && row["task_id"] != strconv.FormatInt(s.TaskID, 10) {
					return fmt.Errorf("archive receipt task mismatch")
				}
			}
		}
	}
	return nil
}

// The server drains executor callbacks first. Row locks and a final comparison
// also reject a receipt/decision that arrived after the package snapshot.
func lockArchiveExecutions(tx *sql.Tx, s *TaskArchiveSnapshot) error {
	for _, table := range []string{"tool_execution_receipts", "execution_recoveries", "task_execution_policies", "task_tool_policies"} {
		suffix := " FOR UPDATE"
		if table == "tool_execution_receipts" || table == "execution_recoveries" {
			suffix = " FOR UPDATE OF x"
		}
		rows, err := tx.Query(archiveExecutionQuery(table)+suffix, strconv.FormatInt(s.TaskID, 10))
		if err != nil {
			return err
		}
		// Closing the result drains this bounded-to-task SELECT, retaining row locks.
		for rows.Next() {
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	rows, err := tx.Query("SELECT id FROM intercept_pending WHERE task_id=$1 FOR UPDATE", strconv.FormatInt(s.TaskID, 10))
	if err != nil {
		return err
	}
	for rows.Next() {
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, table := range archiveExecutionTables {
		var same bool
		q := "SELECT COALESCE(jsonb_agg(to_jsonb(x)),'[]'::jsonb)=$2::jsonb FROM (" + archiveExecutionQuery(table) + ") x"
		if err := tx.QueryRow(q, strconv.FormatInt(s.TaskID, 10), string(s.Tables[table])).Scan(&same); err != nil {
			return err
		}
		if !same {
			return fmt.Errorf("%w: %s changed after snapshot; retry archive", ErrTaskArchiveState, table)
		}
	}
	return nil
}

func restoreArchiveExecutions(tx *sql.Tx, s *TaskArchiveSnapshot) error {
	if s.FormatVersion < 4 {
		return nil
	}
	for _, table := range archiveExecutionTables {
		raw := s.Tables[table]
		if table == "execution_recoveries" {
			rows, err := decodeArchiveRows(raw)
			if err != nil {
				return err
			}
			for _, row := range rows {
				row["claim_token"] = ""
				row["lease_until"] = nil
				if row["state"] == "checking" || row["state"] == "pending" {
					row["state"] = "parked"
				}
				v, _ := jsonInt64(row["version"])
				row["version"] = v + 1
			}
			raw, err = json.Marshal(rows)
			if err != nil {
				return err
			}
		}
		if table == "task_execution_policies" || table == "task_tool_policies" {
			if _, err := tx.Exec("DELETE FROM "+table+" WHERE task_id=$1", s.TaskID); err != nil {
				return err
			}
		}
		if rawRowCount(raw) == 0 {
			continue
		}
		if _, err := tx.Exec("INSERT INTO "+table+" SELECT * FROM json_populate_recordset(NULL::"+table+",$1::json)", string(raw)); err != nil {
			return fmt.Errorf("restore %s: %w", table, err)
		}
	}
	_, err := tx.Exec("UPDATE tasks SET execution_control_version=execution_control_version+1 WHERE id=$1", s.TaskID)
	return err
}

// ArchiveExecutionIDs enumerates only this task's executor-owned spool folders.
func (d *DB) ArchiveExecutionIDs(taskID int64) ([]int64, error) {
	rows, err := d.Query("SELECT approval_id FROM tool_execution_receipts WHERE task_id=$1 ORDER BY approval_id", strconv.FormatInt(taskID, 10))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
