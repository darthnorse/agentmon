// Package directive is the hub's mint side of the access directive. The hub MINTS
// (signs) a short-lived rw grant with the per-server signing key; the agent only
// verifies. The crypto + canonical form live in shared.SignDirective.
package directive

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/google/uuid"

	"agentmon/hubd/internal/db"
	"agentmon/shared"
)

// Expiry is how far ahead a minted directive's Exp sits. Short (well under the
// agent's 5m cap) because it only needs to cover connection establishment.
const Expiry = 60 * time.Second

// Minter builds and signs directives. The seams (Now/NewNonce/NewRequestID) are
// injectable so tests can pin the timestamp and nonce; production uses the wall
// clock, a CSPRNG nonce, and a uuid request id.
type Minter struct {
	Now          func() time.Time
	NewNonce     func() string
	NewRequestID func() string
}

func (m Minter) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m Minter) nonce() (string, error) {
	if m.NewNonce != nil {
		return m.NewNonce(), nil
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		// The nonce is the replay-prevention primitive; never mint with a
		// degraded (all-zero) nonce on a CSPRNG failure — fail the mint instead.
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (m Minter) requestID() string {
	if m.NewRequestID != nil {
		return m.NewRequestID()
	}
	return uuid.NewString()
}

// Mint returns the X-AgentMon-Directive header value and the request id for an
// rw terminal grant on srv's pane. The directive is signed with srv.SigningKey.
func (m Minter) Mint(srv db.Server, principalID, paneID, target string) (header, requestID string, err error) {
	requestID = m.requestID()
	nonce, err := m.nonce()
	if err != nil {
		return "", "", fmt.Errorf("mint nonce: %w", err)
	}
	d := shared.Directive{
		ServerID:    srv.ID,
		Target:      target,
		Resource:    shared.PaneID(srv.ID, target, paneID),
		Mode:        "rw",
		PrincipalID: principalID,
		Action:      "terminal.write",
		Exp:         m.now().Add(Expiry).UTC().Format(time.RFC3339),
		Nonce:       nonce,
		RequestID:   requestID,
	}
	header, err = shared.SignDirective([]byte(srv.SigningKey), d)
	return header, requestID, err
}
