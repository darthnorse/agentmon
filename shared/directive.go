package shared

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
)

// Directive is the short-lived hub→agent access grant (HMAC-signed by the hub,
// verified by the agent). See Phase 1 spec §6.3. Mode is "ro" or "rw".
type Directive struct {
	ServerID    string `json:"serverId"`
	Target      string `json:"target"`
	Resource    string `json:"resource"` // e.g. pane:server-a/default/%3
	Mode        string `json:"mode"`
	PrincipalID string `json:"principalId"`
	Action      string `json:"action"`
	Exp         string `json:"exp"` // RFC3339
	Nonce       string `json:"nonce"`
	RequestID   string `json:"requestId"`
}

// CanonicalJSON is the exact byte sequence that gets HMAC'd. Field order is fixed
// by the struct definition; both hub (sign) and agent (verify) must use this.
func (d Directive) CanonicalJSON() ([]byte, error) { return json.Marshal(d) }

// DirectiveMAC is the single canonical HMAC-SHA256 over a directive's payload.
// Both the hub (SignDirective) and the agent (Verify) compute the tag with this
// so the mint and verify sides cannot silently diverge.
func DirectiveMAC(key, payload []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(payload)
	return h.Sum(nil)
}

// SignDirective returns the X-AgentMon-Directive header value for d:
// base64url(canonicalPayload) + "." + base64url(mac). This is the hub's mint
// primitive; the agent's Verifier parses exactly this form.
func SignDirective(key []byte, d Directive) (string, error) {
	payload, err := d.CanonicalJSON()
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	return enc.EncodeToString(payload) + "." + enc.EncodeToString(DirectiveMAC(key, payload)), nil
}
