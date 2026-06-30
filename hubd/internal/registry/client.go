package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"agentmon/hubd/internal/db"
	"agentmon/shared"
)

type Client struct{ HTTP *http.Client }

var ErrStateUnsupported = errors.New("agent does not support /state")

// ErrInvalidSession maps an agent 400 (bad name, custom command, or cwd outside
// the allow-list). ErrSessionExists maps an agent 409 (duplicate name).
var (
	ErrInvalidSession = errors.New("invalid session request")
	ErrSessionExists  = errors.New("session already exists")
	// ErrNoSession maps an agent 404 from rename (the source session is gone).
	ErrNoSession = errors.New("no such session")
)

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

func (c *Client) CreateSession(ctx context.Context, srv db.Server, target string, req shared.CreateSessionRequest) (shared.CreateSessionResponse, error) {
	u := srv.URL + "/sessions"
	if target != "" {
		u += "?target=" + url.QueryEscape(target) // same idiom as Sessions/State
	}
	body, err := json.Marshal(req)
	if err != nil {
		return shared.CreateSessionResponse{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return shared.CreateSessionResponse{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+srv.Bearer)
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return shared.CreateSessionResponse{}, fmt.Errorf("dial agent %s: %w", srv.ID, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		var out shared.CreateSessionResponse
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return shared.CreateSessionResponse{}, fmt.Errorf("decode agent %s create-session: %w", srv.ID, err)
		}
		return out, nil
	case http.StatusBadRequest:
		return shared.CreateSessionResponse{}, ErrInvalidSession
	case http.StatusConflict:
		return shared.CreateSessionResponse{}, ErrSessionExists
	default:
		return shared.CreateSessionResponse{}, fmt.Errorf("agent %s create-session returned %d", srv.ID, resp.StatusCode)
	}
}

// RenameSession renames session `from` to `to` on the agent's target. Maps the
// agent's 400→ErrInvalidSession, 409→ErrSessionExists, 404→ErrNoSession.
func (c *Client) RenameSession(ctx context.Context, srv db.Server, target, from, to string) error {
	u := srv.URL + "/sessions/rename"
	if target != "" {
		u += "?target=" + url.QueryEscape(target)
	}
	body, err := json.Marshal(shared.RenameSessionRequest{From: from, To: to})
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Authorization", "Bearer "+srv.Bearer)
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return fmt.Errorf("dial agent %s: %w", srv.ID, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		return nil
	case http.StatusBadRequest:
		return ErrInvalidSession
	case http.StatusConflict:
		return ErrSessionExists
	case http.StatusNotFound:
		return ErrNoSession
	default:
		return fmt.Errorf("agent %s rename-session returned %d", srv.ID, resp.StatusCode)
	}
}

func (c *Client) State(ctx context.Context, srv db.Server, target string) (shared.AgentState, error) {
	u := srv.URL + "/state"
	if target != "" {
		u += "?target=" + url.QueryEscape(target)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return shared.AgentState{}, err
	}
	req.Header.Set("Authorization", "Bearer "+srv.Bearer)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return shared.AgentState{}, fmt.Errorf("dial agent %s: %w", srv.ID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return shared.AgentState{}, ErrStateUnsupported
	}
	if resp.StatusCode != http.StatusOK {
		return shared.AgentState{}, fmt.Errorf("agent %s state returned %d", srv.ID, resp.StatusCode)
	}
	var st shared.AgentState
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return shared.AgentState{}, fmt.Errorf("decode agent %s state: %w", srv.ID, err)
	}
	return st, nil
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
