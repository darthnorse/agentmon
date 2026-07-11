package epicfile

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func write(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const sample = `---
title: Mobile session keep-alive
labels: agentmon:epic, pipeline:light
blocked-by: epic-01, #12
---
## Scope
Keep panes mounted.

Acceptance: no flash on switch.
`

func TestParse(t *testing.T) {
	e, err := Parse(write(t, "epic-02-keepalive.md", sample))
	if err != nil {
		t.Fatal(err)
	}
	if e.Title != "Mobile session keep-alive" || e.Issue != 0 {
		t.Fatalf("epic = %+v", e)
	}
	if !reflect.DeepEqual(e.Labels, []string{"agentmon:epic", "pipeline:light"}) {
		t.Fatalf("labels = %v", e.Labels)
	}
	if !reflect.DeepEqual(e.BlockedBy, []string{"epic-01", "#12"}) {
		t.Fatalf("blocked-by = %v", e.BlockedBy)
	}
	if !strings.HasPrefix(e.Body, "## Scope") || !strings.Contains(e.Body, "no flash") {
		t.Fatalf("body = %q", e.Body)
	}
}

func TestParseBracketsTolerated(t *testing.T) {
	e, err := Parse(write(t, "epic-01-x.md", "---\ntitle: T\nlabels: [a, b]\n---\nbody"))
	if err != nil || !reflect.DeepEqual(e.Labels, []string{"a", "b"}) {
		t.Fatalf("labels = %v err=%v", e.Labels, err)
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"no front-matter": "just text",
		"unknown key":     "---\ntitle: T\nassignee: bob\n---\n",
		"no title":        "---\nlabels: a\n---\n",
		"unclosed":        "---\ntitle: T\n",
		"bad issue":       "---\ntitle: T\nissue: soon\n---\n",
	}
	for name, content := range cases {
		if _, err := Parse(write(t, "epic-09-e.md", content)); err == nil {
			t.Fatalf("%s: must error", name)
		}
	}
}

func TestStampIssueInsertsAndReplaces(t *testing.T) {
	p := write(t, "epic-03-s.md", sample)
	if err := StampIssue(p, 41); err != nil {
		t.Fatal(err)
	}
	e, err := Parse(p)
	if err != nil || e.Issue != 41 {
		t.Fatalf("issue = %d err=%v", e.Issue, err)
	}
	if e.Title != "Mobile session keep-alive" || !strings.Contains(e.Body, "## Scope") {
		t.Fatalf("stamp corrupted the file: %+v", e)
	}
	if err := StampIssue(p, 42); err != nil { // replace, not duplicate
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	if strings.Count(string(raw), "issue:") != 1 {
		t.Fatalf("duplicate issue lines:\n%s", raw)
	}
}
