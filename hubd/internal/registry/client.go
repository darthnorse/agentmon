package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"agentmon/hubd/internal/db"
	"agentmon/shared"
)

type Client struct{ HTTP *http.Client }

func NewClient(timeout time.Duration) *Client {
	return &Client{HTTP: &http.Client{Timeout: timeout}}
}

func (c *Client) Sessions(ctx context.Context, srv db.Server, target string) ([]shared.Session, error) {
	u := srv.URL + "/sessions"
	if target != "" {
		u += "?target=" + url.QueryEscape(target)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+srv.Bearer)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dial agent %s: %w", srv.ID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent %s returned %d", srv.ID, resp.StatusCode)
	}
	var list shared.SessionList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("decode agent %s sessions: %w", srv.ID, err)
	}
	out := make([]shared.Session, 0, len(list.Sessions))
	for _, s := range list.Sessions {
		s.Server = srv.ID // stamp the registry id; never trust the agent's self-report
		out = append(out, s)
	}
	return out, nil
}

func (c *Client) Health(ctx context.Context, srv db.Server) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/healthz", nil)
	if err != nil {
		return false
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
