// Package authz is the hub's single authorization chokepoint. Every protected
// REST handler and (in M4) the WS upgrade calls Authorize. The v1 body is a
// trivial single-user allow, but the seam is real: the principal is stamped at
// the edge and every decision flows through here so denies can be audited.
package authz

import "context"

type Principal struct {
	ID          string
	Username    string
	DisplayName string
}

type Action string

const (
	ServerView    Action = "server.view"
	SessionView   Action = "session.view"
	TerminalRead  Action = "terminal.read"
	TerminalWrite Action = "terminal.write"
	AuditRead     Action = "audit.read"
	SessionCreate Action = "session.create"
	SessionRename Action = "session.rename"
	ServerAdmit   Action = "server.admit"
)

type Decision struct {
	Allow  bool
	Reason string
}

// Authorize is the Phase 1 policy: any authenticated single principal is allowed
// every action. The signature carries action+resource so later phases add real
// policy without touching call sites.
func Authorize(ctx context.Context, p Principal, action Action, resource string) (Decision, error) {
	if p.ID == "" {
		return Decision{Allow: false, Reason: "no principal"}, nil
	}
	return Decision{Allow: true}, nil
}
