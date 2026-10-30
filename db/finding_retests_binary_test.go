package db

import (
	"strings"
	"testing"
)

func TestRetestBinaryPersistence(t *testing.T) {
	d, fid := retestDB(t)
	const input = "PNG\x00证据 literal \\u0000 invalid \xff"
	const want = "PNG证据 literal \\u0000 invalid �"
	r, c, created, err := d.CreateFindingRetest(t.Context(), fid, input)
	if err != nil || !created {
		t.Fatalf("create: %v %v", created, err)
	}
	if r.Notes != want {
		t.Fatalf("notes: %q", r.Notes)
	}
	if ok, err := d.StartFindingRetest(t.Context(), r.ID); err != nil || !ok {
		t.Fatalf("start: %v %v", ok, err)
	}
	for range 2 {
		if err := d.RecordFindingRetestResult(t.Context(), c.ID, "reproduced", input, input); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.RecordFindingRetestResult(t.Context(), c.ID, "reproduced", "\x00", "\x00"); err == nil || !strings.Contains(err.Error(), "不能为空") {
		t.Fatalf("empty conclusion validation: %v", err)
	}
	if err := d.FinishFindingRetest(r.ID, "failed", input); err != nil {
		t.Fatal(err)
	}
	got, err := d.FindingRetestForConversation(t.Context(), c.ID)
	if err != nil || got == nil {
		t.Fatalf("read: %+v %v", got, err)
	}
	if got.Summary != want || got.Evidence != want || got.Error != want || got.Status != "failed" {
		t.Fatalf("persisted retest: %+v", got)
	}
	_, next, created, err := d.CreateFindingRetest(t.Context(), fid, "retry")
	if err != nil || !created {
		t.Fatalf("create next: %v %v", created, err)
	}
	if err := d.FailPendingRetestForConversation(next.ID, input); err != nil {
		t.Fatal(err)
	}
	got, err = d.FindingRetestForConversation(t.Context(), next.ID)
	if err != nil || got == nil || got.Error != want || got.Status != "failed" {
		t.Fatalf("pending failure: %+v %v", got, err)
	}
}
