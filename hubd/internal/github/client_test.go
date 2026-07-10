package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeGH records requests and serves canned JSON per path.
func fakeGH(t *testing.T, routes map[string]any, status map[string]int, seen *[]*http.Request) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			*seen = append(*seen, r.Clone(context.Background()))
		}
		key := r.Method + " " + r.URL.Path
		if s, ok := status[key]; ok {
			w.WriteHeader(s)
			return
		}
		body, ok := routes[key]
		if !ok {
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(body)
	}))
}

func TestGetIssueAndAuth(t *testing.T) {
	var seen []*http.Request
	srv := fakeGH(t, map[string]any{
		"GET /repos/o/r/issues/15": map[string]any{
			"number": 15, "title": "GDPR", "body": "Blocked by #13", "state": "open",
			"updated_at": "2026-07-10T10:00:00Z",
			"labels":     []map[string]any{{"name": "agentmon:epic"}, {"name": "pr-gate"}},
		},
	}, nil, &seen)
	defer srv.Close()
	c := NewClient("tok")
	c.Base = srv.URL
	is, err := c.GetIssue(context.Background(), "o/r", 15)
	if err != nil {
		t.Fatal(err)
	}
	if is.Number != 15 || is.Labels[1] != "pr-gate" || is.State != "open" {
		t.Fatalf("got %+v", is)
	}
	if got := seen[0].Header.Get("Authorization"); got != "Bearer tok" {
		t.Fatalf("auth header = %q", got)
	}
	if _, err := c.GetIssue(context.Background(), "o/r", 99); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestListIssuesSinceFiltersPRs(t *testing.T) {
	srv := fakeGH(t, map[string]any{
		"GET /repos/o/r/issues": []map[string]any{
			{"number": 1, "title": "epic", "state": "open", "labels": []map[string]any{}},
			{"number": 2, "title": "a pr", "state": "open", "pull_request": map[string]any{"url": "x"},
				"labels": []map[string]any{}},
		},
	}, nil, nil)
	defer srv.Close()
	c := NewClient("t")
	c.Base = srv.URL
	got, err := c.ListIssuesSince(context.Background(), "o/r", "")
	if err != nil || len(got) != 1 || got[0].Number != 1 {
		t.Fatalf("got %+v err=%v", got, err)
	}
}

func TestGetPullRequestAndChecks(t *testing.T) {
	srv := fakeGH(t, map[string]any{
		"GET /repos/o/r/pulls/58": map[string]any{
			"number": 58, "state": "open", "merged": false, "body": "…verdict…",
			"head": map[string]any{"sha": "abc123", "ref": "epic/15-gdpr"},
		},
		"GET /repos/o/r/commits/abc123/check-runs": map[string]any{
			"check_runs": []map[string]any{
				{"name": "ci", "status": "completed", "conclusion": "success"},
				{"name": "lint", "status": "in_progress", "conclusion": ""},
			},
		},
	}, nil, nil)
	defer srv.Close()
	c := NewClient("t")
	c.Base = srv.URL
	pr, err := c.GetPullRequest(context.Background(), "o/r", 58)
	if err != nil || pr.HeadSHA != "abc123" || pr.HeadRef != "epic/15-gdpr" {
		t.Fatalf("pr=%+v err=%v", pr, err)
	}
	runs, err := c.ListCheckRuns(context.Background(), "o/r", "abc123")
	if err != nil || len(runs) != 2 {
		t.Fatalf("runs=%v err=%v", runs, err)
	}
	green, pending := ChecksState(runs)
	if green || !pending {
		t.Fatalf("green=%v pending=%v", green, pending)
	}
	if g, p := ChecksState(nil); !g || p {
		t.Fatalf("no CI must read green, got green=%v pending=%v", g, p)
	}
}

func TestMergePR(t *testing.T) {
	srv := fakeGH(t,
		map[string]any{"PUT /repos/o/r/pulls/58/merge": map[string]any{"merged": true}},
		map[string]int{"PUT /repos/o/r/pulls/59/merge": 409}, nil)
	defer srv.Close()
	c := NewClient("t")
	c.Base = srv.URL
	if err := c.MergePR(context.Background(), "o/r", 58); err != nil {
		t.Fatal(err)
	}
	if err := c.MergePR(context.Background(), "o/r", 59); !errors.Is(err, ErrNotMergeable) {
		t.Fatalf("want ErrNotMergeable, got %v", err)
	}
}

func TestWriteBackCalls(t *testing.T) {
	var seen []*http.Request
	srv := fakeGH(t, map[string]any{
		"POST /repos/o/r/issues/15/comments":            map[string]any{"id": 1},
		"POST /repos/o/r/issues/15/labels":              []map[string]any{},
		"DELETE /repos/o/r/issues/15/labels/agentmon:x": map[string]any{},
	}, nil, &seen)
	defer srv.Close()
	c := NewClient("t")
	c.Base = srv.URL
	ctx := context.Background()
	if err := c.CreateIssueComment(ctx, "o/r", 15, "hi"); err != nil {
		t.Fatal(err)
	}
	if err := c.AddLabels(ctx, "o/r", 15, []string{"agentmon:merged"}); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveLabel(ctx, "o/r", 15, "agentmon:x"); err != nil {
		t.Fatal(err)
	}
	// RemoveLabel tolerates 404 (label already absent)
	if err := c.RemoveLabel(ctx, "o/r", 15, "gone"); err != nil {
		t.Fatalf("404 remove should be nil, got %v", err)
	}
}
