// Package directive signs and verifies the hub→agent HMAC access directive
// (spec §6.3). Signing (Sign) is the crypto primitive the hub uses to mint a
// directive (wired in M4) and tests use to exercise the verifier. The agent only
// ever Verifies: it checks the HMAC, expiry, server/resource/target match, and
// nonce replay — it never derives user authorization from a directive.
package directive

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"agentmon/shared"
)

var (
	ErrMalformed        = errors.New("directive: malformed header")
	ErrBadSignature     = errors.New("directive: signature mismatch")
	ErrExpired          = errors.New("directive: expired")
	ErrServerMismatch   = errors.New("directive: server mismatch")
	ErrResourceMismatch = errors.New("directive: resource mismatch")
	ErrTargetMismatch   = errors.New("directive: target mismatch")
	ErrBadMode          = errors.New("directive: mode not ro|rw")
	ErrReplay           = errors.New("directive: nonce replay")
)

// maxLifetime caps how far in the future a directive's exp may be. The hub mints
// ~60s expiries; rejecting anything beyond this bounds the nonce cache retention
// and catches a bogus far-future directive. A clock-skew allowance is folded in.
const maxLifetime = 5 * time.Minute

func mac(key, payload []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(payload)
	return h.Sum(nil)
}

// Sign returns the X-AgentMon-Directive header value for d.
func Sign(key []byte, d shared.Directive) (string, error) {
	payload, err := d.CanonicalJSON()
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	return enc.EncodeToString(payload) + "." + enc.EncodeToString(mac(key, payload)), nil
}

// Verifier verifies directives for one agent (server). Safe for concurrent use.
type Verifier struct {
	serverID string
	key      []byte
	now      func() time.Time

	mu   sync.Mutex
	seen map[string]time.Time // nonce -> directive exp (for eviction)
}

func NewVerifier(serverID string, key []byte, now func() time.Time) *Verifier {
	if now == nil {
		now = time.Now
	}
	return &Verifier{serverID: serverID, key: key, now: now, seen: map[string]time.Time{}}
}

// Verify checks the header's signature and fields against the expected resource
// and target, then records the nonce to block replays. The returned directive's
// Mode is authoritative for ro/rw.
func (v *Verifier) Verify(header, wantResource, wantTarget string) (shared.Directive, error) {
	var zero shared.Directive
	p, sigPart, ok := strings.Cut(header, ".")
	if !ok || p == "" || sigPart == "" {
		return zero, ErrMalformed
	}
	enc := base64.RawURLEncoding
	payload, err := enc.DecodeString(p)
	if err != nil {
		return zero, ErrMalformed
	}
	sig, err := enc.DecodeString(sigPart)
	if err != nil {
		return zero, ErrMalformed
	}
	if !hmac.Equal(sig, mac(v.key, payload)) {
		return zero, ErrBadSignature
	}
	var d shared.Directive
	if err := json.Unmarshal(payload, &d); err != nil {
		return zero, ErrMalformed
	}
	if d.ServerID != v.serverID {
		return zero, ErrServerMismatch
	}
	if d.Resource != wantResource {
		return zero, ErrResourceMismatch
	}
	if d.Target != wantTarget {
		return zero, ErrTargetMismatch
	}
	if d.Mode != "ro" && d.Mode != "rw" {
		return zero, ErrBadMode
	}
	exp, err := time.Parse(time.RFC3339, d.Exp)
	if err != nil {
		return zero, ErrMalformed
	}
	now := v.now()
	if !now.Before(exp) { // now >= exp
		return zero, ErrExpired
	}
	if exp.After(now.Add(maxLifetime)) {
		return zero, ErrExpired // far-future exp is treated as invalid
	}
	if err := v.recordNonce(d.Nonce, exp, now); err != nil {
		return zero, err
	}
	return d, nil
}

// recordNonce evicts expired nonces, then rejects a nonce already seen within its
// validity window and otherwise records it until its directive's exp.
func (v *Verifier) recordNonce(nonce string, exp, now time.Time) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	for n, e := range v.seen {
		if !now.Before(e) {
			delete(v.seen, n)
		}
	}
	if _, dup := v.seen[nonce]; dup {
		return ErrReplay
	}
	v.seen[nonce] = exp
	return nil
}
