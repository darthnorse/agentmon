package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmon/agent/internal/epicfile"
)

type ghCall struct {
	name string
	args []string
}

// fakeGH returns URLs for successive `gh issue create` calls and records everything.
func fakeGH(t *testing.T, calls *[]ghCall, issueNums ...int) cmdRunner {
	t.Helper()
	i := 0
	return func(_ string, name string, args ...string) (string, error) {
		*calls = append(*calls, ghCall{name, args})
		if name == "gh" && len(args) > 1 && args[0] == "issue" && args[1] == "create" {
			if i >= len(issueNums) {
				t.Fatal("more creates than expected")
			}
			n := issueNums[i]
			i++
			return fmt.Sprintf("https://github.com/o/r/issues/%d\n", n), nil
		}
		return "", nil
	}
}

func writeEpics(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"epic-01-auth.md":  "---\ntitle: Auth\nlabels: agentmon:epic\n---\nAuth body",
		"epic-02-model.md": "---\ntitle: Model\nlabels: agentmon:epic\nblocked-by: epic-01\n---\nModel body",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestImportCreatesStampsAndLinks(t *testing.T) {
	dir := writeEpics(t)
	var calls []ghCall
	var out bytes.Buffer
	err := importEpics([]string{"--dir", dir, "--repo", "o/r"}, &out, fakeGH(t, &calls, 41, 42))
	if err != nil {
		t.Fatal(err)
	}
	e1, _ := epicfile.Parse(filepath.Join(dir, "epic-01-auth.md"))
	e2, _ := epicfile.Parse(filepath.Join(dir, "epic-02-model.md"))
	if e1.Issue != 41 || e2.Issue != 42 {
		t.Fatalf("stamped issues: %d %d", e1.Issue, e2.Issue)
	}
	var sawEdit bool
	for _, c := range calls {
		if c.args[0] == "issue" && c.args[1] == "edit" {
			sawEdit = true
			joined := strings.Join(c.args, " ")
			if !strings.Contains(joined, "42") || !strings.Contains(joined, "Blocked-by: #41") {
				t.Fatalf("edit call wrong: %v", c.args)
			}
		}
	}
	if !sawEdit {
		t.Fatal("no gh issue edit for the blocked-by pass")
	}
}

func TestImportSkipsStampedFiles(t *testing.T) {
	dir := t.TempDir()
	content := "---\ntitle: Done\nissue: 7\n---\nbody"
	if err := os.WriteFile(filepath.Join(dir, "epic-01-done.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	var calls []ghCall
	var out bytes.Buffer
	if err := importEpics([]string{"--dir", dir, "--repo", "o/r"}, &out, fakeGH(t, &calls)); err != nil {
		t.Fatal(err)
	}
	for _, c := range calls {
		if c.args[1] == "create" {
			t.Fatal("stamped file must not be re-created")
		}
	}
}

func TestImportDryRunCallsNothing(t *testing.T) {
	dir := writeEpics(t)
	var calls []ghCall
	var out bytes.Buffer
	if err := importEpics([]string{"--dir", dir, "--repo", "o/r", "--dry-run"}, &out, fakeGH(t, &calls)); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 {
		t.Fatalf("dry-run must not call gh: %v", calls)
	}
	// Dry-run still previews the dependency pass; unstamped siblings resolve
	// to their symbolic basename.
	if !strings.Contains(out.String(), "Blocked-by: <epic-01-auth>") {
		t.Fatalf("dry-run must preview the planned dependency edit:\n%s", out.String())
	}
}

func TestImportDryRunStillValidatesRefs(t *testing.T) {
	dir := t.TempDir()
	content := "---\ntitle: X\nblocked-by: epic-99\n---\nbody"
	if err := os.WriteFile(filepath.Join(dir, "epic-01-x.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	var calls []ghCall
	var out bytes.Buffer
	if err := importEpics([]string{"--dir", dir, "--repo", "o/r", "--dry-run"}, &out, fakeGH(t, &calls)); err == nil {
		t.Fatal("dry-run must reject an unresolvable blocked-by ref")
	}
}

func TestImportBadRefMakesNoGHCalls(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"epic-01-ok.md":  "---\ntitle: OK\n---\nbody",
		"epic-02-bad.md": "---\ntitle: Bad\nblocked-by: epic-99\n---\nbody",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var calls []ghCall
	var out bytes.Buffer
	err := importEpics([]string{"--dir", dir, "--repo", "o/r"}, &out, fakeGH(t, &calls, 50, 51))
	if err == nil {
		t.Fatal("bad ref must fail the import")
	}
	// Preflight must reject BEFORE any issue is created — a mid-run failure
	// would leave partial remote state.
	if len(calls) != 0 {
		t.Fatalf("no gh call may happen before ref validation: %v", calls)
	}
}

func TestImportSelfRefRejected(t *testing.T) {
	dir := t.TempDir()
	content := "---\ntitle: X\nblocked-by: epic-01\n---\nbody"
	if err := os.WriteFile(filepath.Join(dir, "epic-01-x.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	var calls []ghCall
	var out bytes.Buffer
	err := importEpics([]string{"--dir", dir, "--repo", "o/r"}, &out, fakeGH(t, &calls))
	if err == nil || !strings.Contains(err.Error(), "refers to the epic itself") {
		t.Fatalf("self-reference must be rejected: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("no gh calls on a rejected import: %v", calls)
	}
}

func TestImportCycleRejected(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"epic-01-a.md": "---\ntitle: A\nblocked-by: epic-02\n---\nbody",
		"epic-02-b.md": "---\ntitle: B\nblocked-by: epic-01\n---\nbody",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var calls []ghCall
	var out bytes.Buffer
	err := importEpics([]string{"--dir", dir, "--repo", "o/r"}, &out, fakeGH(t, &calls))
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("a blocked-by cycle must be rejected (it deadlocks the hub queue): %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("no gh calls on a rejected import: %v", calls)
	}
}

func TestImportUnresolvableRefErrors(t *testing.T) {
	dir := t.TempDir()
	content := "---\ntitle: X\nblocked-by: epic-99\n---\nbody"
	if err := os.WriteFile(filepath.Join(dir, "epic-01-x.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	var calls []ghCall
	var out bytes.Buffer
	if err := importEpics([]string{"--dir", dir, "--repo", "o/r"}, &out, fakeGH(t, &calls, 50)); err == nil {
		t.Fatal("unresolvable blocked-by ref must error")
	}
}
