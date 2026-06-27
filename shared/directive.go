package shared

import "encoding/json"

// Directive is the short-lived hub→agent access grant (HMAC-signed by the hub,
// verified by the agent). See Phase 1 spec §6.3. Mode is "ro" or "rw".
type Directive struct {
	ServerID    string `json:"serverId"`
	Target      string `json:"target"`
	Resource    string `json:"resource"` // e.g. pane:server-a/default/%3
	Mode        string `json:"mode"`
	PrincipalID string `json:"principalId"`
	Action      string `json:"action"`
	Exp         string `json:"exp"`   // RFC3339
	Nonce       string `json:"nonce"`
	RequestID   string `json:"requestId"`
}

// CanonicalJSON is the exact byte sequence that gets HMAC'd. Field order is fixed
// by the struct definition; both hub (sign) and agent (verify) must use this.
func (d Directive) CanonicalJSON() ([]byte, error) { return json.Marshal(d) }
