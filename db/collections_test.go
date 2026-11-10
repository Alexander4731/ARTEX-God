package db

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func collectionFixture(t *testing.T) (*DB, *Task, int64, int64) {
	t.Helper()
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	for _, err := range []error{d.EnsureLLMRecordsTable(), d.EnsureLLMUsageTable()} {
		if err != nil {
			t.Fatal(err)
		}
	}
	name := fmt.Sprintf("collection-%d", time.Now().UnixNano())
	aid, err := d.Assets().UpsertRootDomain(UpsertRootDomainReq{Domain: name + ".example"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := d.ImportCollection(name, "fixture", []int64{aid})
	if err != nil {
		t.Fatal(err)
	}
	task, err := d.CreateTaskWithOptions(name, "fixture", TaskCreateOptions{CollectionIDs: []int64{c.ID}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = d.Exec(`DELETE FROM task_archives WHERE task_id=$1`, task.ID)
		_ = d.DeleteTask(task.ID)
		_ = d.DeleteCollection(c.ID)
		_, _ = d.Exec(`DELETE FROM assets WHERE id=$1`, aid)
	})
	return d, task, c.ID, aid
}

func archiveCollectionTask(t *testing.T, d *DB, task *Task) (*TaskArchive, *TaskArchiveSnapshot) {
	t.Helper()
	if err := d.SetPaused(task.ID, true); err != nil {
		t.Fatal(err)
	}
	archive, err := d.QueueTaskArchive(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	job, err := d.ClaimTaskArchiveJob(t.Context())
	if err != nil || job == nil || job.ID != archive.ID {
		t.Fatalf("claim: %+v %v", job, err)
	}
	snapshot, err := d.SnapshotTaskArchive(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.CompleteTaskArchive(archive.ID, snapshot, "fixture.zip", "fixture", 1, 1); err != nil {
		t.Fatal(err)
	}
	return archive, snapshot
}

func restoreCollectionTask(t *testing.T, d *DB, archive *TaskArchive, snapshot *TaskArchiveSnapshot) {
	t.Helper()
	if _, err := d.QueueTaskArchiveRestore(archive.ID); err != nil {
		t.Fatal(err)
	}
	job, err := d.ClaimTaskArchiveJob(t.Context())
	if err != nil || job == nil || job.ID != archive.ID {
		t.Fatalf("restore claim: %+v %v", job, err)
	}
	if _, err := d.RestoreTaskArchive(archive.ID, snapshot, 0); err != nil {
		t.Fatal(err)
	}
}

func TestCollectionTaskSnapshotSurvivesImportAndDeletion(t *testing.T) {
	d, task, cid, _ := collectionFixture(t)
	aid, err := d.Assets().UpsertRootDomain(UpsertRootDomainReq{Domain: fmt.Sprintf("later-%d.example", task.ID)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM assets WHERE id=$1`, aid) })
	if err := d.AddCollectionMembers(cid, []int64{aid}); err != nil {
		t.Fatal(err)
	}
	for _, inherited := range []bool{false, true} {
		var rows []TaskScope
		if inherited {
			rows, err = d.Assets().ListTaskScopeWithSources(task.ID)
		} else {
			rows, err = d.Assets().ListTaskScope(task.ID)
		}
		if err != nil || len(rows) != 1 || rows[0].CollectionID == nil || *rows[0].CollectionID != cid || rows[0].CollectionName != task.Description {
			t.Fatalf("scope: %+v %v", rows, err)
		}
	}
	for _, deleted := range []bool{false, true} {
		if deleted {
			if err := d.DeleteCollection(cid); err != nil {
				t.Fatal(err)
			}
		}
		cov, err := d.Assets().TaskCoverageWithSources(task.ID)
		if err != nil || cov.Denominator != 1 {
			t.Fatalf("coverage after deletion=%v: %+v %v", deleted, cov, err)
		}
	}
	rows, err := d.Assets().ListTaskScope(task.ID)
	if err != nil || len(rows) != 1 || rows[0].CollectionID != nil || rows[0].CollectionName != task.Description {
		t.Fatalf("deleted scope: %+v %v", rows, err)
	}
}

func TestCollectionMembersSurviveArchiveAndTaskDeletion(t *testing.T) {
	d, task, cid, aid := collectionFixture(t)
	archive, snapshot := archiveCollectionTask(t, d, task)
	var count int
	if err := d.QueryRow(`SELECT count(*) FROM asset_collection_members WHERE collection_id=$1 AND asset_id=$2`, cid, aid).Scan(&count); err != nil || count != 1 {
		t.Fatalf("members=%d err=%v", count, err)
	}
	restoreCollectionTask(t, d, archive, snapshot)
	result, err := d.DeleteTaskCascade(task.ID, true, false)
	if err != nil || result.AssetsDeleted != 0 {
		t.Fatalf("delete: %+v %v", result, err)
	}
	if err := d.QueryRow(`SELECT count(*) FROM asset_collection_members WHERE collection_id=$1`, cid).Scan(&count); err != nil || count != 1 {
		t.Fatalf("members after delete=%d err=%v", count, err)
	}
}

func TestCollectionArchiveRestoresDeletedGroupSnapshot(t *testing.T) {
	d, task, cid, _ := collectionFixture(t)
	archive, snapshot := archiveCollectionTask(t, d, task)
	if err := d.DeleteCollection(cid); err != nil {
		t.Fatal(err)
	}
	restoreCollectionTask(t, d, archive, snapshot)
	cov, err := d.Assets().TaskCoverageWithSources(task.ID)
	if err != nil || cov.Denominator != 1 {
		t.Fatalf("restored coverage: %+v %v", cov, err)
	}
}

func TestCollectionArchiveRemapsSnapshotAssetIDs(t *testing.T) {
	raw := json.RawMessage(`[{"kind":"collection","asset_ids":[10,20],"collection_name":"fixture"}]`)
	got, err := remapArchiveAssetReferences(map[string]json.RawMessage{"task_scope": raw}, map[int64]int64{10: 30})
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		AssetIDs []int64 `json:"asset_ids"`
	}
	if err := json.Unmarshal(got["task_scope"], &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || len(rows[0].AssetIDs) != 2 || rows[0].AssetIDs[0] != 30 || rows[0].AssetIDs[1] != 20 {
		t.Fatalf("remapped: %s", got["task_scope"])
	}
}

func TestCollectionImportRollsBackMemberFailure(t *testing.T) {
	d, _, cid, aid := collectionFixture(t)
	var name string
	if err := d.QueryRow(`SELECT name FROM asset_collections WHERE id=$1`, cid).Scan(&name); err != nil {
		t.Fatal(err)
	}
	// Reject this fixture's member insert inside the database, after the upsert.
	if _, err := d.Exec(fmt.Sprintf(`CREATE FUNCTION reject_collection_fixture() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.collection_id=%d THEN RAISE EXCEPTION 'fixture member failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER reject_collection_fixture BEFORE INSERT ON asset_collection_members FOR EACH ROW EXECUTE FUNCTION reject_collection_fixture()`, cid)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = d.Exec(`DROP TRIGGER IF EXISTS reject_collection_fixture ON asset_collection_members; DROP FUNCTION IF EXISTS reject_collection_fixture()`)
	})
	if _, err := d.ImportCollection(name, "must rollback", []int64{aid}); err == nil {
		t.Fatal("expected member failure")
	}
	var query string
	if err := d.QueryRow(`SELECT query FROM asset_collections WHERE id=$1`, cid).Scan(&query); err != nil || query != "fixture" {
		t.Fatalf("upsert was not rolled back: %q %v", query, err)
	}
}
