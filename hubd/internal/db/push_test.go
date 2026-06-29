package db

import (
	"context"
	"sort"
	"testing"
)

func TestPushUpsertAndList(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	sub := PushSubscription{
		PrincipalID: "u1",
		Endpoint:    "https://push.example/abc",
		P256dh:      "key1",
		Auth:        "auth1",
		UserAgent:   "Firefox",
		CreatedAt:   "2026-06-29 10:00:00.000",
	}
	if err := d.UpsertSubscription(ctx, sub); err != nil {
		t.Fatalf("UpsertSubscription: %v", err)
	}

	got, err := d.ListSubscriptionsForPrincipal(ctx, "u1")
	if err != nil {
		t.Fatalf("ListSubscriptionsForPrincipal: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 row, got %d: %+v", len(got), got)
	}
	if got[0] != sub {
		t.Fatalf("got %+v want %+v", got[0], sub)
	}
}

func TestPushUpsertUpdatesOnConflict(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	first := PushSubscription{
		PrincipalID: "u1",
		Endpoint:    "https://push.example/abc",
		P256dh:      "key1",
		Auth:        "auth1",
		UserAgent:   "Firefox",
		CreatedAt:   "2026-06-29 10:00:00.000",
	}
	if err := d.UpsertSubscription(ctx, first); err != nil {
		t.Fatalf("first UpsertSubscription: %v", err)
	}

	// Same endpoint, different keys/principal/ua → must update, not duplicate.
	second := PushSubscription{
		PrincipalID: "u2",
		Endpoint:    "https://push.example/abc",
		P256dh:      "key2",
		Auth:        "auth2",
		UserAgent:   "Chrome",
		CreatedAt:   "2026-06-29 11:00:00.000",
	}
	if err := d.UpsertSubscription(ctx, second); err != nil {
		t.Fatalf("second UpsertSubscription: %v", err)
	}

	// Original principal no longer owns the endpoint.
	if rows, err := d.ListSubscriptionsForPrincipal(ctx, "u1"); err != nil {
		t.Fatalf("ListSubscriptionsForPrincipal u1: %v", err)
	} else if len(rows) != 0 {
		t.Fatalf("expected u1 to have no rows after re-assign, got %+v", rows)
	}

	got, err := d.ListSubscriptionsForPrincipal(ctx, "u2")
	if err != nil {
		t.Fatalf("ListSubscriptionsForPrincipal u2: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 row (updated, not duplicated), got %d: %+v", len(got), got)
	}
	// created_at is preserved from first insert (first-seen time); everything
	// else is updated from the conflicting upsert.
	want := second
	want.CreatedAt = first.CreatedAt
	if got[0] != want {
		t.Fatalf("got %+v want %+v", got[0], want)
	}
}

func TestPushListEmptyUserAgent(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	sub := PushSubscription{
		PrincipalID: "u9",
		Endpoint:    "https://push.example/noua",
		P256dh:      "k",
		Auth:        "a",
		UserAgent:   "", // stored as NULL, returned as ""
		CreatedAt:   "2026-06-29 12:00:00.000",
	}
	if err := d.UpsertSubscription(ctx, sub); err != nil {
		t.Fatalf("UpsertSubscription: %v", err)
	}
	got, err := d.ListSubscriptionsForPrincipal(ctx, "u9")
	if err != nil {
		t.Fatalf("ListSubscriptionsForPrincipal: %v", err)
	}
	if len(got) != 1 || got[0] != sub {
		t.Fatalf("got %+v want %+v", got, sub)
	}
}

func TestPushDeleteSubscription(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	sub := PushSubscription{
		PrincipalID: "u1",
		Endpoint:    "https://push.example/del",
		P256dh:      "k",
		Auth:        "a",
		UserAgent:   "UA",
		CreatedAt:   "2026-06-29 10:00:00.000",
	}
	if err := d.UpsertSubscription(ctx, sub); err != nil {
		t.Fatalf("UpsertSubscription: %v", err)
	}

	if err := d.DeleteSubscription(ctx, sub.Endpoint); err != nil {
		t.Fatalf("DeleteSubscription: %v", err)
	}
	got, err := d.ListSubscriptionsForPrincipal(ctx, "u1")
	if err != nil {
		t.Fatalf("ListSubscriptionsForPrincipal: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 rows after delete, got %+v", got)
	}

	// Deleting an absent endpoint is a no-op (no error).
	if err := d.DeleteSubscription(ctx, "https://push.example/missing"); err != nil {
		t.Fatalf("DeleteSubscription(absent) should be no-op, got: %v", err)
	}
}

func TestPushPrincipalIDsWithSubscriptions(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	subs := []PushSubscription{
		{PrincipalID: "u1", Endpoint: "e1", P256dh: "k", Auth: "a", UserAgent: "UA", CreatedAt: "2026-06-29 10:00:00.000"},
		{PrincipalID: "u1", Endpoint: "e2", P256dh: "k", Auth: "a", UserAgent: "UA", CreatedAt: "2026-06-29 10:00:01.000"},
		{PrincipalID: "u2", Endpoint: "e3", P256dh: "k", Auth: "a", UserAgent: "UA", CreatedAt: "2026-06-29 10:00:02.000"},
	}
	for _, s := range subs {
		if err := d.UpsertSubscription(ctx, s); err != nil {
			t.Fatalf("UpsertSubscription %+v: %v", s, err)
		}
	}

	ids, err := d.PrincipalIDsWithSubscriptions(ctx)
	if err != nil {
		t.Fatalf("PrincipalIDsWithSubscriptions: %v", err)
	}
	sort.Strings(ids)
	want := []string{"u1", "u2"}
	if len(ids) != len(want) {
		t.Fatalf("expected distinct principals %v, got %v", want, ids)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, ids)
		}
	}
}

func TestPushPrincipalIDsWithSubscriptionsEmpty(t *testing.T) {
	d := openTestDB(t)
	ids, err := d.PrincipalIDsWithSubscriptions(context.Background())
	if err != nil {
		t.Fatalf("PrincipalIDsWithSubscriptions: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("expected no principals, got %v", ids)
	}
}

func TestPushLoadOrCreateVAPID(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	calls := 0
	gen := func() (priv, pub string, err error) {
		calls++
		return "priv", "pub", nil
	}

	keys, err := d.LoadOrCreateVAPID(ctx, gen, "2026-06-29 10:00:00.000")
	if err != nil {
		t.Fatalf("LoadOrCreateVAPID (first): %v", err)
	}
	if keys.Public != "pub" || keys.Private != "priv" {
		t.Fatalf("unexpected keys: %+v", keys)
	}
	if calls != 1 {
		t.Fatalf("expected gen called once, got %d", calls)
	}

	// Second call must return the SAME persisted keys and NOT call gen again.
	keys2, err := d.LoadOrCreateVAPID(ctx, gen, "2026-06-29 11:00:00.000")
	if err != nil {
		t.Fatalf("LoadOrCreateVAPID (second): %v", err)
	}
	if keys2 != keys {
		t.Fatalf("second call returned different keys: %+v vs %+v", keys2, keys)
	}
	if calls != 1 {
		t.Fatalf("expected gen NOT called again, got %d calls", calls)
	}
}

// TestPushTablesExist verifies migration 0004 created the push tables/index.
func TestPushTablesExist(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	for _, name := range []string{"push_subscriptions", "push_vapid"} {
		var got string
		err := d.sql.QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&got)
		if err != nil {
			t.Fatalf("table %q not found after migration: %v", name, err)
		}
	}
	var idx string
	if err := d.sql.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='index' AND name='idx_push_principal'`).Scan(&idx); err != nil {
		t.Fatalf("idx_push_principal index not found after migration: %v", err)
	}
}
