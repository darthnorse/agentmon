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
