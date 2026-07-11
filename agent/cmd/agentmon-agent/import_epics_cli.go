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
// blocked-by pass recomputes each body from the FILE, so re-runs converge for
// added/changed deps. (Removing EVERY dep from an already-imported file is the
// one non-converging case: the tool never reads remote bodies, so the issue's
// old Blocked-by line survives — edit the issue by hand.) Two phases because
// refs may point forward: create everything first, then rewrite Blocked-by
// lines once all numbers exist. All refs are validated BEFORE any gh mutation.
func importEpics(args []string, stdout io.Writer, run cmdRunner) error {
	fs := flag.NewFlagSet("import-epics", flag.ContinueOnError)
	fs.SetOutput(stdout)
	dir := fs.String("dir", "docs/plan", "directory holding epic-*.md files")
	repo := fs.String("repo", "", "owner/name (default: derived from the cwd's git remote origin)")
	dryRun := fs.Bool("dry-run", false, "print planned gh calls without making them")
	if err := fs.Parse(args); err != nil {
		return err
	}
	r, err := resolveRepoFlag(*repo, run)
	if err != nil {
		return err
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
	// Preflight, before ANY gh mutation: every ref must resolve, none may be
	// a self-reference, and the in-set dependency graph must be acyclic. A
	// bad ref discovered mid-run would leave issues already created; a cycle
	// would import cleanly and then deadlock on the hub (deps are satisfied
	// only by merged/closed issues, and queued epics are outside stall
	// detection — a cycle waits forever with no error anywhere).
	if err := validateRefs(epics); err != nil {
		return err
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
			return fmt.Errorf("created #%d but failed to stamp %s (add 'issue: %d' by hand before re-running, or the next run creates a duplicate): %w",
				e.Issue, e.Path, e.Issue, err)
		}
		fmt.Fprintf(stdout, "+ %s → #%d\n", filepath.Base(e.Path), e.Issue)
	}
	// Phase 2: resolve blocked-by refs and write the dependency lines the hub
	// parses (ParseBlockedBy: "Blocked-by: #a, #b"). Dry-run MUST still walk
	// this phase for unstamped epics (Issue==0 because phase 1 only printed):
	// it previews the planned edits with symbolic refs.
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
				target = symbolicRef(e.Path)
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

// epicBase is an epic file's ref name: the basename without ".md".
func epicBase(path string) string {
	return strings.TrimSuffix(filepath.Base(path), ".md")
}

// symbolicRef is the dry-run placeholder for a not-yet-created issue.
func symbolicRef(path string) string {
	return "<" + epicBase(path) + ">"
}

// findRef locates a blocked-by ref: numeric "#12"/"12" → (nil, 12); a sibling
// file ref → (match, 0), by unique basename-prefix match. Missing/ambiguous →
// error.
func findRef(ref string, epics []*epicfile.Epic) (*epicfile.Epic, int, error) {
	if n, err := strconv.Atoi(strings.TrimPrefix(ref, "#")); err == nil && n > 0 {
		return nil, n, nil
	}
	var match *epicfile.Epic
	for _, e := range epics {
		if base := epicBase(e.Path); base == ref || strings.HasPrefix(base, ref+"-") {
			if match != nil {
				return nil, 0, fmt.Errorf("blocked-by ref %q is ambiguous", ref)
			}
			match = e
		}
	}
	if match == nil {
		return nil, 0, fmt.Errorf("blocked-by ref %q matches no epic file", ref)
	}
	return match, 0, nil
}

// validateRefs rejects unresolvable refs, self-references, and cycles across
// the imported set. Numeric refs are mapped back into the set via stamped
// issue numbers where possible; numbers unknown to the set are external and
// cannot participate in an in-set cycle.
func validateRefs(epics []*epicfile.Epic) error {
	index := make(map[*epicfile.Epic]int, len(epics))
	byIssue := make(map[int]int, len(epics))
	for i, e := range epics {
		index[e] = i
		if e.Issue != 0 {
			byIssue[e.Issue] = i
		}
	}
	deps := make([][]int, len(epics))
	for i, e := range epics {
		for _, ref := range e.BlockedBy {
			match, n, err := findRef(ref, epics)
			if err != nil {
				return fmt.Errorf("%s: %w", e.Path, err)
			}
			j := -1
			if match != nil {
				j = index[match]
			} else if k, ok := byIssue[n]; ok {
				j = k
			}
			if j == i {
				return fmt.Errorf("%s: blocked-by ref %q refers to the epic itself", e.Path, ref)
			}
			if j >= 0 {
				deps[i] = append(deps[i], j)
			}
		}
	}
	state := make([]int, len(epics)) // 0 unvisited, 1 in-stack, 2 done
	var visit func(int) error
	visit = func(i int) error {
		state[i] = 1
		for _, j := range deps[i] {
			if state[j] == 1 {
				return fmt.Errorf("%s: blocked-by cycle via %s", epics[i].Path, epicBase(epics[j].Path))
			}
			if state[j] == 0 {
				if err := visit(j); err != nil {
					return err
				}
			}
		}
		state[i] = 2
		return nil
	}
	for i := range epics {
		if state[i] == 0 {
			if err := visit(i); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolveRef maps a blocked-by ref to its "#N" token. An unstamped sibling is
// an error on a real run (phase 1 stamps before phase 2 reaches it) but legal
// in dry-run, where creates were only printed: it resolves to the symbolic
// "<basename>".
func resolveRef(ref string, epics []*epicfile.Epic, dryRun bool) (string, error) {
	match, n, err := findRef(ref, epics)
	if err != nil {
		return "", err
	}
	if match == nil {
		return "#" + strconv.Itoa(n), nil
	}
	if match.Issue == 0 {
		if dryRun {
			return symbolicRef(match.Path), nil
		}
		return "", fmt.Errorf("blocked-by ref %q resolves to unstamped %s", ref, filepath.Base(match.Path))
	}
	return "#" + strconv.Itoa(match.Issue), nil
}
