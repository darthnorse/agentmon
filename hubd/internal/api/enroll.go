package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"regexp"

	"agentmon/hubd/internal/audit"
	"agentmon/hubd/internal/authn"
	"agentmon/hubd/internal/db"
)

const agentPort = "8377"

var hostnameRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]{0,251}[A-Za-z0-9])?$`)

// os/agentVersion are shown in the operator's `server list`; restrict to a safe
// printable charset (empty allowed) so an enroller can't inject terminal escapes
// or forge table rows via embedded control/tab/newline bytes.
var fieldRe = regexp.MustCompile(`^[A-Za-z0-9._+-]{0,64}$`)

// EnrollStore is the DB surface the enroll handler needs.
type EnrollStore interface {
	GetServer(ctx context.Context, id string) (db.Server, error)
	EnrollServer(ctx context.Context, s db.Server) error
}

type EnrollDeps struct {
	Servers             EnrollStore
	Audit               *audit.Recorder
	TrustForwardedProto bool
}

type enrollReq struct {
	Hostname     string `json:"hostname"`
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	AgentVersion string `json:"agentVersion"`
	Target       struct {
		OSUser string `json:"osUser"`
		Socket string `json:"socket"`
		Label  string `json:"label"`
	} `json:"target"`
}

type enrollResp struct {
	ServerID   string `json:"serverId"`
	Bearer     string `json:"bearer"`
	SigningKey string `json:"signingKey"`
}

// Handler is open (no RequireAuth); it is mounted behind the onboarding
// rate-limiter. It records a pending server and returns generated credentials.
func (e EnrollDeps) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req enrollReq
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "bad request")
			return
		}
		if !hostnameRe.MatchString(req.Hostname) {
			writeJSONError(w, http.StatusBadRequest, "bad request")
			return
		}
		if req.Arch != "amd64" && req.Arch != "arm64" {
			writeJSONError(w, http.StatusBadRequest, "bad request")
			return
		}
		if !fieldRe.MatchString(req.OS) || !fieldRe.MatchString(req.AgentVersion) {
			writeJSONError(w, http.StatusBadRequest, "bad request")
			return
		}
		id := req.Hostname // default id = hostname

		// Duplicate id → 409 (operator must revoke + rm, or pass --hostname).
		if _, err := e.Servers.GetServer(r.Context(), id); err == nil {
			writeJSONError(w, http.StatusConflict, "already enrolled; revoke + rm first, or pass --hostname")
			return
		}

		bearer, err := genSecret()
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		signingKey, err := genSecret()
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}

		// The dialled URL is the agent's real peer IP (trusted-proxy-aware) +
		// the fixed agent port. net.JoinHostPort brackets IPv6 literals.
		// When trust_forwarded_proto is false (direct LAN) ClientIP returns the
		// RemoteAddr host; when true (behind Caddy) it returns the last
		// X-Forwarded-For hop, which is the real peer the proxy appended.
		clientIP := authn.ClientIP(r, e.TrustForwardedProto)
		url := "http://" + net.JoinHostPort(clientIP, agentPort)

		srv := db.Server{
			ID: id, Name: req.Hostname, Hostname: req.Hostname, URL: url,
			Status: "pending", Bearer: bearer, SigningKey: signingKey,
			OS: req.OS, Arch: req.Arch, AgentVersion: req.AgentVersion,
		}
		if err := e.Servers.EnrollServer(r.Context(), srv); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		e.Audit.ServerEnroll(r.Context(), id, req.Hostname, clientIP)
		writeJSON(w, http.StatusOK, enrollResp{ServerID: id, Bearer: bearer, SigningKey: signingKey})
	}
}

// genSecret returns 32 bytes of CSPRNG as base64url (no padding).
func genSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
