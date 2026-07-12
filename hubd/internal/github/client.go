// Package github is a minimal GitHub REST v3 client — only the calls the
// orchestrator needs, hand-rolled to avoid a dependency. Fine-grained PAT.
package github

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	ErrNotFound     = errors.New("github: not found")
	ErrNotMergeable = errors.New("github: not mergeable")
)

var (
	repoRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	refRe  = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
)

// validRepo/validRef reject anything that could re-route the PAT-authenticated
// request to an unintended endpoint should a repo/ref ever arrive from
// less-trusted input. Defense-in-depth: current callers are admin config.
// IsValidRepo reports whether repo is a bare owner/name slug — exported for
// API-edge validation so malformed repos are rejected before they become
// permanently-failing project rows.
func IsValidRepo(repo string) bool { return repoRe.MatchString(repo) }

func validRepo(repo string) error {
	if !repoRe.MatchString(repo) {
		return fmt.Errorf("github: invalid repo %q", repo)
	}
	return nil
}

func validRef(ref string) error {
	if ref == "" || strings.Contains(ref, "..") || !refRe.MatchString(ref) {
		return fmt.Errorf("github: invalid ref %q", ref)
	}
	return nil
}

type Issue struct {
	Number    int
	Title     string
	Body      string
	State     string
	Labels    []string
	UpdatedAt string
	IsPR      bool // the issues API returns PRs too; callers must be able to tell
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
	is := Issue{Number: w.Number, Title: w.Title, Body: w.Body, State: w.State,
		UpdatedAt: w.UpdatedAt, IsPR: w.PullRequest != nil}
	for _, l := range w.Labels {
		is.Labels = append(is.Labels, l.Name)
	}
	return is
}

func (c *Client) GetIssue(ctx context.Context, repo string, num int) (Issue, error) {
	if err := validRepo(repo); err != nil {
		return Issue{}, err
	}
	var w wireIssue
	_, err := c.do(ctx, "GET", fmt.Sprintf("/repos/%s/issues/%d", repo, num), nil, &w, 200)
	if err != nil {
		return Issue{}, err
	}
	return w.issue(), nil
}

// ListIssuesLabeledSince scopes the listing to one label — the sync loop only
// mirrors labeled issues, and label scoping keeps boot syncs on mature repos
// to a page or two instead of the full state=all history.
func (c *Client) ListIssuesLabeledSince(ctx context.Context, repo, label, since string) ([]Issue, error) {
	if err := validRepo(repo); err != nil {
		return nil, err
	}
	const perPage, maxPages = 100, 20
	var out []Issue
	for page := 1; ; page++ {
		q := url.Values{"state": {"all"}, "per_page": {strconv.Itoa(perPage)}, "page": {strconv.Itoa(page)}}
		if since != "" {
			q.Set("since", since)
		}
		if label != "" {
			q.Set("labels", label)
		}
		var ws []wireIssue
		_, err := c.do(ctx, "GET", fmt.Sprintf("/repos/%s/issues?%s", repo, q.Encode()), nil, &ws, 200)
		if err != nil {
			return nil, err
		}
		for _, w := range ws {
			if w.PullRequest != nil { // the issues API interleaves PRs; skip them
				continue
			}
			out = append(out, w.issue())
		}
		if len(ws) < perPage {
			return out, nil
		}
		if page == maxPages {
			return nil, fmt.Errorf("github: issue listing for %s exceeded %d pages; refusing to silently truncate", repo, maxPages)
		}
	}
}

func (c *Client) GetPullRequest(ctx context.Context, repo string, num int) (PullRequest, error) {
	if err := validRepo(repo); err != nil {
		return PullRequest{}, err
	}
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

// ListCheckRuns FAILS CLOSED on a partial view: if total_count exceeds what
// one page returned, it errors rather than letting the gate read a hidden
// failing run as green. (per_page=100; >100 check runs on one commit is the
// error path by design — escalate, don't guess.)
func (c *Client) ListCheckRuns(ctx context.Context, repo, ref string) ([]CheckRun, error) {
	if err := validRepo(repo); err != nil {
		return nil, err
	}
	if err := validRef(ref); err != nil {
		return nil, err
	}
	var w struct {
		TotalCount int `json:"total_count"`
		CheckRuns  []struct {
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		} `json:"check_runs"`
	}
	_, err := c.do(ctx, "GET", fmt.Sprintf("/repos/%s/commits/%s/check-runs?per_page=100", repo, ref), nil, &w, 200)
	if err != nil {
		return nil, err
	}
	if w.TotalCount > len(w.CheckRuns) {
		return nil, fmt.Errorf("github: %d of %d check runs returned for %s; refusing to gate on a partial view",
			len(w.CheckRuns), w.TotalCount, ref)
	}
	var out []CheckRun
	for _, r := range w.CheckRuns {
		out = append(out, CheckRun{Name: r.Name, Status: r.Status, Conclusion: r.Conclusion})
	}
	return out, nil
}

// MergePR squash-merges pinned to the evaluated head SHA: GitHub rejects with
// 409 when the branch moved after the gate evaluated it, closing the
// check-to-merge race (author pushes between evaluation and merge).
func (c *Client) MergePR(ctx context.Context, repo string, num int, sha string) error {
	if err := validRepo(repo); err != nil {
		return err
	}
	body := map[string]string{"merge_method": "squash"}
	if sha != "" {
		body["sha"] = sha
	}
	status, err := c.do(ctx, "PUT", fmt.Sprintf("/repos/%s/pulls/%d/merge", repo, num), body, nil, 200)
	if err != nil && (status == 405 || status == 409) {
		return ErrNotMergeable
	}
	return err
}

func (c *Client) CreateIssueComment(ctx context.Context, repo string, num int, body string) error {
	if err := validRepo(repo); err != nil {
		return err
	}
	_, err := c.do(ctx, "POST", fmt.Sprintf("/repos/%s/issues/%d/comments", repo, num),
		map[string]string{"body": body}, nil, 201, 200)
	return err
}

func (c *Client) AddLabels(ctx context.Context, repo string, num int, labels []string) error {
	if err := validRepo(repo); err != nil {
		return err
	}
	_, err := c.do(ctx, "POST", fmt.Sprintf("/repos/%s/issues/%d/labels", repo, num),
		map[string][]string{"labels": labels}, nil, 200, 201)
	return err
}

func (c *Client) RemoveLabel(ctx context.Context, repo string, num int, label string) error {
	if err := validRepo(repo); err != nil {
		return err
	}
	_, err := c.do(ctx, "DELETE",
		fmt.Sprintf("/repos/%s/issues/%d/labels/%s", repo, num, url.PathEscape(label)), nil, nil, 200, 204)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// ErrTooLarge marks a contents fetch rejected by the size cap.
var ErrTooLarge = errors.New("github: file too large")

// maxContentsBytes caps plan-doc fetches (spec §5.2). GitHub's JSON contents
// API itself omits content above 1 MiB; we cap far below that.
const maxContentsBytes = 256 << 10 // 256 KiB

// validPath guards a repo-relative file path interpolated into the URL —
// same defense-in-depth role as validRef, plus traversal/absolute rejection.
func validPath(p string) error {
	if p == "" || len(p) > 512 || strings.HasPrefix(p, "/") || strings.Contains(p, "..") || !refRe.MatchString(p) {
		return fmt.Errorf("github: invalid path %q", p)
	}
	return nil
}

type wireContents struct {
	Type     string `json:"type"`
	Encoding string `json:"encoding"`
	Size     int    `json:"size"`
	Content  string `json:"content"`
}

// GetContents fetches one file's bytes at ref via the JSON contents API —
// do() is JSON-only by design, and this reuses its auth/error/status handling
// (raw-accept mode would need a parallel request path). Directories decode
// into a JSON array and fail loudly rather than returning garbage.
func (c *Client) GetContents(ctx context.Context, repo, path, ref string) ([]byte, error) {
	if err := validRepo(repo); err != nil {
		return nil, err
	}
	if err := validPath(path); err != nil {
		return nil, err
	}
	if err := validRef(ref); err != nil {
		return nil, err
	}
	var w wireContents
	if _, err := c.do(ctx, "GET", fmt.Sprintf("/repos/%s/contents/%s?ref=%s", repo, path, url.QueryEscape(ref)), nil, &w, 200); err != nil {
		return nil, err
	}
	if w.Type != "" && w.Type != "file" {
		return nil, fmt.Errorf("github: %s is not a file", path)
	}
	if w.Size > maxContentsBytes || w.Encoding == "none" {
		return nil, ErrTooLarge
	}
	if w.Encoding != "base64" {
		return nil, fmt.Errorf("github: unexpected contents encoding %q", w.Encoding)
	}
	b, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(w.Content, "\n", ""))
	if err != nil {
		return nil, fmt.Errorf("github: decode contents: %w", err)
	}
	if len(b) > maxContentsBytes {
		return nil, ErrTooLarge
	}
	return b, nil
}
