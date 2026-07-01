// Package audit records security-relevant events to the append-only audit_log.
// Writes never include secrets or raw keystrokes; the session name (if any) goes
// in the JSON meta. Append failures are logged, never propagated to the caller —
// a broken audit write must not break the request it describes.
package audit

import (
	"context"
	"encoding/json"
	"log"

	"github.com/google/uuid"

	"agentmon/hubd/internal/authz"
	"agentmon/hubd/internal/db"
)

type Sink interface {
	Append(ctx context.Context, e db.AuditEntry) error
}

type Recorder struct{ sink Sink }

func NewRecorder(s Sink) *Recorder { return &Recorder{sink: s} }

func (r *Recorder) write(ctx context.Context, e db.AuditEntry) {
	e.ID = uuid.NewString()
	if err := r.sink.Append(ctx, e); err != nil {
		log.Printf("audit: append failed (action=%s result=%s): %v", e.Action, e.Result, err)
	}
}

func (r *Recorder) LoginSuccess(ctx context.Context, principalID, ip, ua string) {
	r.write(ctx, db.AuditEntry{PrincipalID: principalID, Action: "login.success",
		Resource: "user:" + principalID, Result: "allow", IP: ip, UserAgent: ua})
}

func (r *Recorder) LoginFailure(ctx context.Context, username, ip, ua string) {
	r.write(ctx, db.AuditEntry{Action: "login.failure",
		Resource: "user:" + username, Result: "deny", IP: ip, UserAgent: ua})
}

func (r *Recorder) Deny(ctx context.Context, principalID string, action authz.Action, resource, ip, ua, meta string) {
	r.write(ctx, db.AuditEntry{PrincipalID: principalID, Action: string(action),
		Resource: resource, Result: "deny", IP: ip, UserAgent: ua, Meta: meta})
}

func (r *Recorder) TerminalOpen(ctx context.Context, principalID, resource, mode, ip, ua string) {
	r.write(ctx, db.AuditEntry{PrincipalID: principalID, Action: "terminal.open",
		Resource: resource, Result: "allow", IP: ip, UserAgent: ua, Meta: mode})
}

func (r *Recorder) SessionCreate(ctx context.Context, principalID, resource, sessionName, ip, ua string) {
	meta, err := json.Marshal(map[string]string{"session": sessionName})
	if err != nil {
		meta = []byte("{}")
	}
	r.write(ctx, db.AuditEntry{PrincipalID: principalID, Action: "session.create",
		Resource: resource, Result: "allow", IP: ip, UserAgent: ua, Meta: string(meta)})
}

func (r *Recorder) SessionRename(ctx context.Context, principalID, resource, from, to, ip, ua string) {
	meta, err := json.Marshal(map[string]string{"from": from, "to": to})
	if err != nil {
		meta = []byte("{}")
	}
	r.write(ctx, db.AuditEntry{PrincipalID: principalID, Action: "session.rename",
		Resource: resource, Result: "allow", IP: ip, UserAgent: ua, Meta: string(meta)})
}

func (r *Recorder) SessionKill(ctx context.Context, principalID, resource, sessionName, ip, ua string) {
	meta, err := json.Marshal(map[string]string{"session": sessionName})
	if err != nil {
		meta = []byte("{}")
	}
	r.write(ctx, db.AuditEntry{PrincipalID: principalID, Action: "session.kill",
		Resource: resource, Result: "allow", IP: ip, UserAgent: ua, Meta: string(meta)})
}

func (r *Recorder) PasswordChange(ctx context.Context, principalID, ip, ua string) {
	r.write(ctx, db.AuditEntry{PrincipalID: principalID, Action: "auth.password_change",
		Resource: "user:" + principalID, Result: "allow", IP: ip, UserAgent: ua})
}

func (r *Recorder) ServerEnroll(ctx context.Context, id, hostname, ip string) {
	r.write(ctx, db.AuditEntry{Action: "server.enroll",
		Resource: "server:" + id, Result: "allow", IP: ip, Meta: hostname})
}

func (r *Recorder) ServerApprove(ctx context.Context, id, hostname string) {
	r.write(ctx, db.AuditEntry{Action: "server.approve",
		Resource: "server:" + id, Result: "allow", Meta: hostname})
}

func (r *Recorder) ServerRevoke(ctx context.Context, id, hostname string) {
	r.write(ctx, db.AuditEntry{Action: "server.revoke",
		Resource: "server:" + id, Result: "allow", Meta: hostname})
}

func (r *Recorder) ServerRemove(ctx context.Context, id, hostname string) {
	r.write(ctx, db.AuditEntry{Action: "server.remove",
		Resource: "server:" + id, Result: "allow", Meta: hostname})
}
