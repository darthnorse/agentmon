package db

import (
	"context"
	"testing"
)

func TestUpsertAndGetSeen(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	s := PrincipalSeen{
		PrincipalID:     "u1",
		ServerID:        "srvX",
		TargetID:        "",
		Session:         "api",
		LastSeenEventID: "e42",
		LastFocusedAt:   "2026-06-29 10:00:05.000",
	}
	if err := d.UpsertSeen(ctx, s); err != nil {
		t.Fatalf("UpsertSeen: %v", err)
	}

	got, ok, err := d.GetSeen(ctx, "u1", "srvX", "", "api")
	if err != nil || !ok {
		t.Fatalf("GetSeen ok=%v err=%v", ok, err)
	}
	if got != s {
		t.Fatalf("got %+v want %+v", got, s)
	}
}

func TestUpsertSeenUpdatesOnConflict(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	s1 := PrincipalSeen{
		PrincipalID:     "u1",
		ServerID:        "srvX",
		TargetID:        "",
		Session:         "api",
		LastSeenEventID: "e1",
		LastFocusedAt:   "2026-06-29 10:00:01.000",
	}
	if err := d.UpsertSeen(ctx, s1); err != nil {
		t.Fatalf("first UpsertSeen: %v", err)
	}

	s2 := PrincipalSeen{
		PrincipalID:     "u1",
		ServerID:        "srvX",
		TargetID:        "",
		Session:         "api",
		LastSeenEventID: "e99",
		LastFocusedAt:   "2026-06-29 11:00:00.000",
	}
	if err := d.UpsertSeen(ctx, s2); err != nil {
		t.Fatalf("second UpsertSeen: %v", err)
	}

	got, ok, err := d.GetSeen(ctx, "u1", "srvX", "", "api")
	if err != nil || !ok {
		t.Fatalf("GetSeen ok=%v err=%v", ok, err)
	}
	if got.LastFocusedAt != s2.LastFocusedAt || got.LastSeenEventID != s2.LastSeenEventID {
		t.Fatalf("expected updated row: got %+v", got)
	}
}

func TestUpsertSeenNullableEventID(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	s := PrincipalSeen{
		PrincipalID:     "u2",
		ServerID:        "srvY",
		TargetID:        "",
		Session:         "web",
		LastSeenEventID: "", // nullable — stored as NULL, returned as ""
		LastFocusedAt:   "2026-06-29 09:00:00.000",
	}
	if err := d.UpsertSeen(ctx, s); err != nil {
		t.Fatalf("UpsertSeen: %v", err)
	}

	got, ok, err := d.GetSeen(ctx, "u2", "srvY", "", "web")
	if err != nil || !ok {
		t.Fatalf("GetSeen ok=%v err=%v", ok, err)
	}
	if got != s {
		t.Fatalf("got %+v want %+v", got, s)
	}
}

func TestListSeenForPrincipal(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	rows := []PrincipalSeen{
		{PrincipalID: "u1", ServerID: "srvA", TargetID: "", Session: "api", LastSeenEventID: "e1", LastFocusedAt: "2026-06-29 10:00:00.000"},
		{PrincipalID: "u1", ServerID: "srvA", TargetID: "", Session: "web", LastSeenEventID: "e2", LastFocusedAt: "2026-06-29 11:00:00.000"},
		{PrincipalID: "u2", ServerID: "srvA", TargetID: "", Session: "api", LastSeenEventID: "e3", LastFocusedAt: "2026-06-29 12:00:00.000"},
	}
	for _, r := range rows {
		if err := d.UpsertSeen(ctx, r); err != nil {
			t.Fatalf("UpsertSeen %+v: %v", r, err)
		}
	}

	got, err := d.ListSeenForPrincipal(ctx, "u1")
	if err != nil {
		t.Fatalf("ListSeenForPrincipal: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 rows for u1, got %d: %+v", len(got), got)
	}
	for _, g := range got {
		if g.PrincipalID != "u1" {
			t.Errorf("unexpected principal %q in result", g.PrincipalID)
		}
	}
}

func TestGetSeenNoRows(t *testing.T) {
	d := openTestDB(t)
	_, ok, err := d.GetSeen(context.Background(), "nobody", "srvZ", "", "nope")
	if err != nil || ok {
		t.Fatalf("expected (_, false, nil), got ok=%v err=%v", ok, err)
	}
}
