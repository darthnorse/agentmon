// Package epicfile parses the docs/plan/epic-NN-<slug>.md files emitted by
// the plan-epics skill and stamps created issue numbers back into them: the
// file is the epic's birth certificate, and a stamped file is skipped on
// re-import (design doc §10). The front-matter is a deliberately strict
// key: value format — NOT YAML — so a typo'd dial fails the import instead of
// silently dropping.
package epicfile

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Epic struct {
	Path      string
	Title     string
	Labels    []string
	BlockedBy []string // raw refs: "#12", "12", or a sibling file ref "epic-01"
	Issue     int      // 0 until stamped
	Body      string
}

// Parse reads one epic file. Contract:
//
//	---
//	title: Session keep-alive          (required)
//	labels: agentmon:epic, plan-gate   (optional; commas; [ ] tolerated)
//	blocked-by: epic-01, #12           (optional; commas; [ ] tolerated)
//	issue: 42                          (absent until stamped by import)
//	---
//	<markdown body: scope, acceptance criteria, constraints, decisions>
func Parse(path string) (Epic, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Epic{}, err
	}
	lines := strings.Split(string(raw), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return Epic{}, fmt.Errorf("%s: missing front-matter open '---'", path)
	}
	e := Epic{Path: path}
	i := 1
	for ; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "---" {
			break
		}
		if line == "" {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			return Epic{}, fmt.Errorf("%s: bad front-matter line %q", path, line)
		}
		val = strings.TrimSpace(val)
		switch strings.TrimSpace(key) {
		case "title":
			e.Title = val
		case "labels":
			e.Labels = splitList(val)
		case "blocked-by":
			e.BlockedBy = splitList(val)
		case "issue":
			n, err := strconv.Atoi(val)
			if err != nil || n <= 0 {
				return Epic{}, fmt.Errorf("%s: bad issue number %q", path, val)
			}
			e.Issue = n
		default:
			return Epic{}, fmt.Errorf("%s: unknown front-matter key %q", path, strings.TrimSpace(key))
		}
	}
	if i == len(lines) {
		return Epic{}, fmt.Errorf("%s: missing front-matter close '---'", path)
	}
	if e.Title == "" {
		return Epic{}, fmt.Errorf("%s: title is required", path)
	}
	e.Body = strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
	return e, nil
}

func splitList(v string) []string {
	v = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(v), "["), "]")
	var out []string
	for _, p := range strings.Split(v, ",") {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// StampIssue rewrites the file with `issue: n` in the front-matter, replacing
// an existing issue line or inserting one before the closing ---.
func StampIssue(path string, n int) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(raw), "\n")
	stamp := fmt.Sprintf("issue: %d", n)
	replaced := false
	for i := 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "issue:") {
			lines[i] = stamp
			replaced = true
			continue
		}
		if trimmed == "---" {
			if !replaced {
				lines = append(lines[:i], append([]string{stamp}, lines[i:]...)...)
			}
			return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
		}
	}
	return fmt.Errorf("%s: missing front-matter close '---'", path)
}
