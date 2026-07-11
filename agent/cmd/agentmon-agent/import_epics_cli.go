package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"agentmon/agent/internal/epicfile"
)

// cmdRunner runs one external command in dir and returns its stdout — the DI
// seam that keeps the gh/git flows testable. Shared with the doctor (Task 16).
type cmdRunner func(dir, name string, args ...string) (string, error)

func execRunner(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, bytes.TrimSpace(ee.Stderr))
		}
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return string(out), nil
}

func importEpicsMain(args []string, stdout io.Writer) error {
	return importEpics(args, stdout, execRunner)
}

var issueURLRe = regexp.MustCompile(`/issues/(\d+)\s*$`)

// importEpics turns docs/plan/epic-*.md files into GitHub issues (design doc
// §10). Idempotent: files already stamped with `issue: N` are skipped, and the
// blocked-by pass recomputes each body from the FILE, so re-runs converge
// instead of accumulating. Two phases because refs may point forward: create
// everything first, then rewrite Blocked-by lines once all numbers exist.
func importEpics(args []string, stdout io.Writer, run cmdRunner) error {
	fs := flag.NewFlagSet("import-epics", flag.ContinueOnError)
	fs.SetOutput(stdout)
	dir := fs.String("dir", "docs/plan", "directory holding epic-*.md files")
	repo := fs.String("repo", "", "owner/name (default: derived from the cwd's git remote origin)")
	dryRun := fs.Bool("dry-run", false, "print planned gh calls without making them")
	if err := fs.Parse(args); err != nil {
		return err
	}
	r := *repo
	if r == "" {
		var err error
		if r, err = repoFromGit("."); err != nil {
			return err
		}
	}
	paths, err := filepath.Glob(filepath.Join(*dir, "epic-*.md"))
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return fmt.Errorf("no epic-*.md files in %s", *dir)
	}
	sort.Strings(paths)
	epics := make([]*epicfile.Epic, 0, len(paths))
	for _, p := range paths {
		e, err := epicfile.Parse(p)
		if err != nil {
			return err
		}
		epics = append(epics, &e)
	}
	// Phase 1: create + stamp.
	for _, e := range epics {
		if e.Issue != 0 {
			fmt.Fprintf(stdout, "= %s already imported as #%d\n", filepath.Base(e.Path), e.Issue)
			continue
		}
		ghArgs := []string{"issue", "create", "--repo", r, "--title", e.Title, "--body", e.Body}
		for _, l := range e.Labels {
			ghArgs = append(ghArgs, "--label", l)
		}
		if *dryRun {
			fmt.Fprintf(stdout, "[dry-run] gh %s\n", strings.Join(ghArgs, " "))
			continue
		}
		out, err := run(".", "gh", ghArgs...)
		if err != nil {
			return fmt.Errorf("create %s: %w", e.Path, err)
		}
		m := issueURLRe.FindStringSubmatch(strings.TrimSpace(out))
		if m == nil {
			return fmt.Errorf("create %s: cannot parse issue number from gh output %q", e.Path, out)
		}
		e.Issue, _ = strconv.Atoi(m[1])
		if err := epicfile.StampIssue(e.Path, e.Issue); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "+ %s → #%d\n", filepath.Base(e.Path), e.Issue)
	}
	// Phase 2: resolve blocked-by refs and write the dependency lines the hub
	// parses (ParseBlockedBy: "Blocked-by: #a, #b"). Dry-run MUST still walk
	// this phase for unstamped epics (Issue==0 because phase 1 only printed):
	// its whole point is validating refs before a real run creates issues.
	for _, e := range epics {
		if len(e.BlockedBy) == 0 || (e.Issue == 0 && !*dryRun) {
			continue
		}
		refs := make([]string, 0, len(e.BlockedBy))
		for _, ref := range e.BlockedBy {
			tok, err := resolveRef(ref, epics, *dryRun)
			if err != nil {
				return fmt.Errorf("%s: %w", e.Path, err)
			}
			refs = append(refs, tok)
		}
		if *dryRun {
			target := "#" + strconv.Itoa(e.Issue)
			if e.Issue == 0 {
				target = "<" + strings.TrimSuffix(filepath.Base(e.Path), ".md") + ">"
			}
			fmt.Fprintf(stdout, "[dry-run] gh issue edit %s --body … (Blocked-by: %s)\n", target, strings.Join(refs, ", "))
			continue
		}
		body := e.Body + "\n\nBlocked-by: " + strings.Join(refs, ", ")
		if _, err := run(".", "gh", "issue", "edit", strconv.Itoa(e.Issue), "--repo", r, "--body", body); err != nil {
			return fmt.Errorf("edit #%d: %w", e.Issue, err)
		}
		fmt.Fprintf(stdout, "~ #%d Blocked-by: %s\n", e.Issue, strings.Join(refs, ", "))
	}
	return nil
}

// resolveRef maps a blocked-by ref to its "#N" token: "#12"/"12" directly;
// "epic-01" by unique basename-prefix match against the sibling files. Missing
// and ambiguous refs are ALWAYS errors. An unstamped sibling is an error on a
// real run (phase 1 stamps before phase 2 reaches it) but legal in dry-run,
// where creates were only printed: it resolves to a symbolic "<basename>".
func resolveRef(ref string, epics []*epicfile.Epic, dryRun bool) (string, error) {
	if n, err := strconv.Atoi(strings.TrimPrefix(ref, "#")); err == nil && n > 0 {
		return "#" + strconv.Itoa(n), nil
	}
	var match *epicfile.Epic
	for _, e := range epics {
		base := strings.TrimSuffix(filepath.Base(e.Path), ".md")
		if base == ref || strings.HasPrefix(base, ref+"-") {
			if match != nil {
				return "", fmt.Errorf("blocked-by ref %q is ambiguous", ref)
			}
			match = e
		}
	}
	if match == nil {
		return "", fmt.Errorf("blocked-by ref %q matches no epic file", ref)
	}
	if match.Issue == 0 {
		if dryRun {
			return "<" + strings.TrimSuffix(filepath.Base(match.Path), ".md") + ">", nil
		}
		return "", fmt.Errorf("blocked-by ref %q resolves to unstamped %s", ref, filepath.Base(match.Path))
	}
	return "#" + strconv.Itoa(match.Issue), nil
}
