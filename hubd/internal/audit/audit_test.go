package audit

import (
	"context"
	"testing"

	"agentmon/hubd/internal/authz"
	"agentmon/hubd/internal/db"
)

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
