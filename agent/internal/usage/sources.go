package usage

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Sources struct{ Claude, Codex []string }

// openTranscriptFDs returns .jsonl files held open by pid or any descendant.
// This binds the PARENT transcript to the exact runner process, which is
// session-safe even when concurrent attempts share a project dir.
func openTranscriptFDs(pid int) []string {
	seen := map[string]bool{}
	for _, p := range append([]int{pid}, descendants(pid)...) {
		entries, _ := os.ReadDir("/proc/" + strconv.Itoa(p) + "/fd")
		for _, e := range entries {
			target, err := os.Readlink("/proc/" + strconv.Itoa(p) + "/fd/" + e.Name())
			if err == nil && strings.HasSuffix(target, ".jsonl") {
				seen[target] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	return out
}

// descendants returns every descendant of pid (children, grandchildren, and
// so on) by walking the FULL process-tree subtree rooted at pid — not just
// one level. /proc is read exactly once (not once per node, unlike a naive
// recursive re-scan) into a pid->children map, which the walk then reuses.
func descendants(pid int) []int {
	children := map[int][]int{}
	procs, _ := os.ReadDir("/proc")
	for _, pe := range procs {
		cpid, err := strconv.Atoi(pe.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile("/proc/" + pe.Name() + "/stat")
		if err != nil {
			continue
		}
		// stat: "pid (comm) state ppid ..." — ppid is field 4, but comm may hold spaces.
		r := strings.LastIndex(string(b), ")")
		if r <= 0 {
			continue
		}
		fields := strings.Fields(string(b)[r+1:])
		if len(fields) < 2 {
			continue
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		children[ppid] = append(children[ppid], cpid)
	}

	var out []int
	queue := []int{pid}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, c := range children[p] {
			out = append(out, c)
			queue = append(queue, c)
		}
	}
	return out
}

// enumerateChildRollouts returns Codex rollouts under codexRoot whose recorded
// cwd == worktree and mtime >= since.
func enumerateChildRollouts(codexRoot, worktree string, since time.Time) []string {
	var out []string
	filepath.WalkDir(codexRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".jsonl") {
			return nil
		}
		if fi, e := d.Info(); e == nil && fi.ModTime().Before(since) {
			return nil
		}
		if rolloutCwd(p) == worktree {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// isCodexPath reports whether f is a Codex rollout (lives under codexRoot) as
// opposed to a Claude transcript.
func isCodexPath(path, codexRoot string) bool {
	return strings.HasPrefix(path, codexRoot)
}

// claudeEncodeCwd mirrors Claude Code's project-directory naming: every "/"
// and "." in cwd becomes "-". Confirmed empirically on this host, e.g.
// "/root/agentmon" -> "-root-agentmon" and
// "/root/agentmon/spike-0.5/scratch" -> "-root-agentmon-spike-0-5-scratch".
func claudeEncodeCwd(cwd string) string {
	return strings.NewReplacer("/", "-", ".", "-").Replace(cwd)
}

// enumerateChildTranscripts returns Claude .jsonl transcripts under
// <claudeRoot>/<encoded cwd>/ with mtime >= since. Unlike Codex rollouts,
// Claude encodes cwd into the project DIRECTORY NAME rather than a payload
// field, so this is a glob rather than a walk-and-match. Over-inclusion
// (parent + subagent transcripts sharing the dir) is safe: Aggregate dedups
// every Claude row globally by message.id.
func enumerateChildTranscripts(claudeRoot, cwd string, since time.Time) []string {
	dir := filepath.Join(claudeRoot, claudeEncodeCwd(cwd))
	matches, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	var out []string
	for _, p := range matches {
		fi, err := os.Stat(p)
		if err != nil || fi.ModTime().Before(since) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// subagentTranscripts returns .jsonl transcripts under
// <parentPath-without-.jsonl>/subagents/ — where /multi-review lens subagent
// transcripts are nested on disk (verified: <claudeRoot>/<encoded-cwd>/
// <parent-session-uuid>/subagents/agent-*.jsonl). Deriving the subagents dir
// from the parent's own fd-bound path (rather than a directory-wide glob)
// binds subagents to THIS session, avoiding cross-session contamination when
// concurrent attempts share a project cwd.
func subagentTranscripts(parentPath string) []string {
	if !strings.HasSuffix(parentPath, ".jsonl") {
		return nil
	}
	dir := strings.TrimSuffix(parentPath, ".jsonl")
	matches, _ := filepath.Glob(filepath.Join(dir, "subagents", "*.jsonl"))
	return matches
}

func rolloutCwd(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var l struct {
			Payload struct {
				Cwd string `json:"cwd"`
			} `json:"payload"`
		}
		if json.Unmarshal(sc.Bytes(), &l) == nil && l.Payload.Cwd != "" {
			return l.Payload.Cwd
		}
	}
	return ""
}
