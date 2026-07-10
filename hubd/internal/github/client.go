// Package github is a minimal GitHub REST v3 client — only the calls the
// orchestrator needs, hand-rolled to avoid a dependency. Fine-grained PAT.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

var (
	ErrNotFound     = errors.New("github: not found")
	ErrNotMergeable = errors.New("github: not mergeable")
)

type Issue struct {
	Number    int
	Title     string
	Body      string
	State     string
	Labels    []string
	UpdatedAt string
}

type PullRequest struct {
	Number  int
	State   string
	Merged  bool
	Body    string
	HeadSHA string
	HeadRef string
}

type CheckRun struct {
	Name       string
	Status     string // queued | in_progress | completed
	Conclusion string // success | failure | neutral | skipped | …
}

// ChecksState folds check runs into (green, pending). No runs at all reads
// green: a repo without CI must still pass the verdict gate, which fails closed.
func ChecksState(runs []CheckRun) (green, pending bool) {
	green = true
	for _, r := range runs {
		if r.Status != "completed" {
			return false, true
		}
		switch r.Conclusion {
		case "success", "neutral", "skipped":
		default:
			green = false
		}
	}
	return green, false
}

type Client struct {
	Base  string
	Token string
	HTTP  *http.Client
}

func NewClient(token string) *Client {
	return &Client{
		Base:  "https://api.github.com",
		Token: token,
		HTTP:  &http.Client{Timeout: 15 * time.Second},
	}
}

// do performs one API call; out may be nil. okStatus lists acceptable codes.
func (c *Client) do(ctx context.Context, method, path string, in, out any, okStatus ...int) (int, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	for _, s := range okStatus {
		if resp.StatusCode == s {
			if out != nil {
				return resp.StatusCode, json.NewDecoder(resp.Body).Decode(out)
			}
			return resp.StatusCode, nil
		}
	}
	if resp.StatusCode == http.StatusNotFound {
		return resp.StatusCode, ErrNotFound
	}
	return resp.StatusCode, fmt.Errorf("github: %s %s → %d", method, path, resp.StatusCode)
}

type wireIssue struct {
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	State       string    `json:"state"`
	UpdatedAt   string    `json:"updated_at"`
	PullRequest *struct{} `json:"pull_request,omitempty"`
	Labels      []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

func (w wireIssue) issue() Issue {
	is := Issue{Number: w.Number, Title: w.Title, Body: w.Body, State: w.State, UpdatedAt: w.UpdatedAt}
	for _, l := range w.Labels {
		is.Labels = append(is.Labels, l.Name)
	}
	return is
}

func (c *Client) GetIssue(ctx context.Context, repo string, num int) (Issue, error) {
	var w wireIssue
	_, err := c.do(ctx, "GET", fmt.Sprintf("/repos/%s/issues/%d", repo, num), nil, &w, 200)
	if err != nil {
		return Issue{}, err
	}
	return w.issue(), nil
}

func (c *Client) ListIssuesSince(ctx context.Context, repo, since string) ([]Issue, error) {
	q := url.Values{"state": {"all"}, "per_page": {"100"}}
	if since != "" {
		q.Set("since", since)
	}
	var ws []wireIssue
	_, err := c.do(ctx, "GET", fmt.Sprintf("/repos/%s/issues?%s", repo, q.Encode()), nil, &ws, 200)
	if err != nil {
		return nil, err
	}
	var out []Issue
	for _, w := range ws {
		if w.PullRequest != nil { // the issues API interleaves PRs; skip them
			continue
		}
		out = append(out, w.issue())
	}
	return out, nil
}

func (c *Client) GetPullRequest(ctx context.Context, repo string, num int) (PullRequest, error) {
	var w struct {
		Number int    `json:"number"`
		State  string `json:"state"`
		Merged bool   `json:"merged"`
		Body   string `json:"body"`
		Head   struct {
			SHA string `json:"sha"`
			Ref string `json:"ref"`
		} `json:"head"`
	}
	_, err := c.do(ctx, "GET", fmt.Sprintf("/repos/%s/pulls/%d", repo, num), nil, &w, 200)
	if err != nil {
		return PullRequest{}, err
	}
	return PullRequest{Number: w.Number, State: w.State, Merged: w.Merged,
		Body: w.Body, HeadSHA: w.Head.SHA, HeadRef: w.Head.Ref}, nil
}

func (c *Client) ListCheckRuns(ctx context.Context, repo, ref string) ([]CheckRun, error) {
	var w struct {
		CheckRuns []struct {
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		} `json:"check_runs"`
	}
	_, err := c.do(ctx, "GET", fmt.Sprintf("/repos/%s/commits/%s/check-runs", repo, ref), nil, &w, 200)
	if err != nil {
		return nil, err
	}
	var out []CheckRun
	for _, r := range w.CheckRuns {
		out = append(out, CheckRun{Name: r.Name, Status: r.Status, Conclusion: r.Conclusion})
	}
	return out, nil
}

func (c *Client) MergePR(ctx context.Context, repo string, num int) error {
	status, err := c.do(ctx, "PUT", fmt.Sprintf("/repos/%s/pulls/%d/merge", repo, num),
		map[string]string{"merge_method": "squash"}, nil, 200)
	if err != nil && (status == 405 || status == 409) {
		return ErrNotMergeable
	}
	return err
}

func (c *Client) CreateIssueComment(ctx context.Context, repo string, num int, body string) error {
	_, err := c.do(ctx, "POST", fmt.Sprintf("/repos/%s/issues/%d/comments", repo, num),
		map[string]string{"body": body}, nil, 201, 200)
	return err
}

func (c *Client) AddLabels(ctx context.Context, repo string, num int, labels []string) error {
	_, err := c.do(ctx, "POST", fmt.Sprintf("/repos/%s/issues/%d/labels", repo, num),
		map[string][]string{"labels": labels}, nil, 200, 201)
	return err
}

func (c *Client) RemoveLabel(ctx context.Context, repo string, num int, label string) error {
	_, err := c.do(ctx, "DELETE",
		fmt.Sprintf("/repos/%s/issues/%d/labels/%s", repo, num, url.PathEscape(label)), nil, nil, 200, 204)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}
