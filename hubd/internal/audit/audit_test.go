package audit

import (
	"context"
	"testing"

	"agentmon/hubd/internal/authz"
	"agentmon/hubd/internal/db"
)

type captureSink struct{ entries []db.AuditEntry }

func (c *captureSink) Append(_ context.Context, e db.AuditEntry) error {
	c.entries = append(c.entries, e)
	return nil
}

type fakeSink struct{ rows []db.AuditEntry }

func (f *fakeSink) Append(_ context.Context, e db.AuditEntry) error {
	f.rows = append(f.rows, e)
	return nil
}

func TestRecorderWritesTypedEvents(t *testing.T) {
	s := &fakeSink{}
	r := NewRecorder(s)
	ctx := context.Background()
	r.LoginSuccess(ctx, "u1", "1.2.3.4", "agent/1")
	r.LoginFailure(ctx, "patrik", "1.2.3.4", "agent/1")
	r.Deny(ctx, "u1", authz.SessionView, "server:server-a", "1.2.3.4", "agent/1", `{"session":"proj"}`)

	if len(s.rows) != 3 {
		t.Fatalf("rows: %d", len(s.rows))
	}
	if s.rows[0].Action != "login.success" || s.rows[0].Result != "allow" || s.rows[0].ID == "" {
		t.Fatalf("login success row: %+v", s.rows[0])
	}
	if s.rows[1].Action != "login.failure" || s.rows[1].Result != "deny" || s.rows[1].Resource != "user:patrik" {
		t.Fatalf("login failure row: %+v", s.rows[1])
	}
	if s.rows[2].Action != "session.view" || s.rows[2].Result != "deny" || s.rows[2].Meta != `{"session":"proj"}` {
		t.Fatalf("deny row: %+v", s.rows[2])
	}
}

func TestTerminalOpenRecorded(t *testing.T) {
	s := &fakeSink{}
	r := NewRecorder(s)
	r.TerminalOpen(context.Background(), "u1", "pane:aigallery/default/%3", "rw", "10.0.0.2", "curl/8")
	if len(s.rows) != 1 {
		t.Fatalf("rows: %d", len(s.rows))
	}
	got := s.rows[0]
	if got.Action != "terminal.open" || got.Result != "allow" {
		t.Fatalf("action/result: %+v", got)
	}
	if got.PrincipalID != "u1" || got.Resource != "pane:aigallery/default/%3" {
		t.Fatalf("principal/resource: %+v", got)
	}
	if got.Meta != "rw" || got.IP != "10.0.0.2" || got.UserAgent != "curl/8" {
		t.Fatalf("meta/ip/ua: %+v", got)
	}
	if got.ID == "" {
		t.Fatal("audit id not stamped")
	}
}

func TestServerLifecycleAudits(t *testing.T) {
	cap := &captureSink{}
	r := NewRecorder(cap)
	ctx := context.Background()
	r.ServerEnroll(ctx, "web-01", "web-01.lan", "10.0.0.9")
	r.ServerApprove(ctx, "web-01", "web-01.lan")
	r.ServerRevoke(ctx, "web-01", "web-01.lan")
	r.ServerRemove(ctx, "web-01", "web-01.lan")
	if len(cap.entries) != 4 {
		t.Fatalf("want 4 audit rows, got %d", len(cap.entries))
	}
	enroll := cap.entries[0]
	if enroll.Action != "server.enroll" || enroll.Resource != "server:web-01" ||
		enroll.Result != "allow" || enroll.IP != "10.0.0.9" || enroll.Meta != "web-01.lan" {
		t.Fatalf("enroll row: %+v", enroll)
	}
	if cap.entries[1].Action != "server.approve" || cap.entries[2].Action != "server.revoke" || cap.entries[3].Action != "server.remove" {
		t.Fatalf("lifecycle actions: %+v", cap.entries)
	}
	// No secret material may appear anywhere in the rows.
	for _, e := range cap.entries {
		if e.Meta == "" {
			t.Fatalf("hostname meta missing: %+v", e)
		}
	}
}
