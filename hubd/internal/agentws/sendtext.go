package agentws

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/gorilla/websocket"

	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/directive"
	"agentmon/shared"
)

// SessionPane finds the named session's first pane and the session's
// AGENT-RESOLVED target label. The label — never a project's raw Target
// config ("" = agent default) — is what the agent verifies directives
// against; minting with the raw value 403s on every default-target project.
func SessionPane(sessions []shared.Session, name string) (paneID, target string, ok bool) {
	for _, s := range sessions {
		if s.Name != name {
			continue
		}
		for _, w := range s.Windows {
			if len(w.Panes) > 0 {
				return w.Panes[0].ID, s.Target, true
			}
		}
	}
	return "", "", false
}

// SendText injects text into a session's first pane over the agent's rw WS —
// the same channel a browser terminal uses, so no new agent surface and no
// new credentials. The directive and dial target come from the session's
// resolved label (see SessionPane).
func SendText(ctx context.Context, srv db.Server, minter *directive.Minter, principalID, session, text string, sessions []shared.Session) error {
	paneID, target, ok := SessionPane(sessions, session)
	if !ok {
		return fmt.Errorf("agentws: session %q has no pane", session)
	}
	header, reqID, err := minter.Mint(srv, principalID, paneID, target)
	if err != nil {
		return fmt.Errorf("agentws: mint: %w", err)
	}
	base, err := url.Parse(srv.URL)
	if err != nil || base.Host == "" {
		return fmt.Errorf("agentws: bad server url")
	}
	scheme := "ws"
	if base.Scheme == "https" {
		scheme = "wss"
	}
	u := scheme + "://" + base.Host + "/panes/" + url.PathEscape(paneID) + "/io?target=" + url.QueryEscape(target) + "&mode=rw"
	h := http.Header{}
	h.Set("Authorization", "Bearer "+srv.Bearer)
	h.Set("X-AgentMon-Directive", header)
	h.Set("X-AgentMon-Request-Id", reqID)
	conn, resp, err := websocket.DefaultDialer.DialContext(ctx, u, h)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("agentws: dial %d: %w", resp.StatusCode, err)
		}
		return fmt.Errorf("agentws: dial: %w", err)
	}
	defer conn.Close()
	return conn.WriteMessage(websocket.BinaryMessage, []byte(text))
}
