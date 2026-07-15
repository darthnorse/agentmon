package db

import (
	"context"
	"testing"
)

func TestUpsertUsageIdempotent(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	row := UsageRow{ProjectID: "p", ProjectName: "P", Repo: "o/r", IssueNumber: 7, Attempt: 1,
		Stage: "reviewing", CapturedAt: "2026-07-14T10:00:00Z", Provider: "claude", Model: "m", Output: 50}
	if err := d.UpsertUsage(ctx, row); err != nil {
		t.Fatal(err)
	}
	row.Output = 99 // same key, corrected value (recovery on redelivery)
	if err := d.UpsertUsage(ctx, row); err != nil {
		t.Fatal(err)
	}
	rows, err := d.ListEpicUsage(ctx, "p", 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Output != 99 {
		t.Fatalf("want 1 row output=99, got %+v", rows)
	}
}

// TestListEpicUsageOrdersSameSecondRowsByInsertion guards Fix 7's ORDER BY
// tie-break: two rows sharing one captured_at second (different stage, so
// distinct rows under the UNIQUE key) must come back in the order they were
// INSERTED (rowid), not any incidental ordering. Stage names are chosen so
// insertion order and alphabetical order disagree — if the query ever
// dropped the trailing `, rowid` and something else (e.g. SQLite's default
// row order) happened to sort alphabetically, this would catch it.
func TestListEpicUsageOrdersSameSecondRowsByInsertion(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	first := UsageRow{ProjectID: "p", ProjectName: "P", Repo: "o/r", IssueNumber: 7, Attempt: 1,
		Stage: "zzz-inserted-first", CapturedAt: "2026-07-14T10:00:00Z", Provider: "claude", Model: "m1", Output: 1}
	second := UsageRow{ProjectID: "p", ProjectName: "P", Repo: "o/r", IssueNumber: 7, Attempt: 1,
		Stage: "aaa-inserted-second", CapturedAt: "2026-07-14T10:00:00Z", Provider: "claude", Model: "m2", Output: 2}
	if err := d.UpsertUsage(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := d.UpsertUsage(ctx, second); err != nil {
		t.Fatal(err)
	}
	rows, err := d.ListEpicUsage(ctx, "p", 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d: %+v", len(rows), rows)
	}
	if rows[0].Stage != "zzz-inserted-first" || rows[1].Stage != "aaa-inserted-second" {
		t.Fatalf("same-second rows must tie-break by insertion order (rowid), got %q then %q", rows[0].Stage, rows[1].Stage)
	}
}
