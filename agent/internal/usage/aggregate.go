package usage

import (
	"os"
	"path/filepath"

	"agentmon/shared"
)

// Aggregate sums usage across all of an attempt's sources into one entry per
// (provider,model). Source PATHS are deduped first (the same file can be
// discovered twice — e.g. a Codex rollout matching both the /proc-fd scan and
// the cwd walk — so each path is parsed at most once). Claude rows are ALSO
// deduped by message.id GLOBALLY, which additionally catches the same row
// repeating within one file; Codex rollouts have no per-row id, so path dedup
// is what prevents a re-discovered rollout's cumulative total from being
// summed twice.
func Aggregate(s Sources) []shared.Usage {
	type key struct{ provider, model string }
	acc := map[key]*shared.Usage{}
	add := func(m MsgUsage) {
		k := key{m.Provider, m.Model}
		u := acc[k]
		if u == nil {
			u = &shared.Usage{Provider: m.Provider, Model: m.Model}
			acc[k] = u
		}
		u.Input += m.Input
		u.Output += m.Output
		u.CacheRead += m.CacheRead
		u.CacheWrite += m.CacheWrite
	}

	seenPath := map[string]bool{} // dedup source paths, regardless of how they were discovered
	seenID := map[string]bool{}   // global Claude message.id dedup
	for _, p := range s.Claude {
		cp := filepath.Clean(p)
		if seenPath[cp] {
			continue
		}
		seenPath[cp] = true
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		rows, _ := ParseClaude(f)
		f.Close()
		for _, r := range rows {
			if r.ID != "" && seenID[r.ID] {
				continue
			}
			if r.ID != "" {
				seenID[r.ID] = true
			}
			add(r)
		}
	}
	for _, p := range s.Codex {
		cp := filepath.Clean(p)
		if seenPath[cp] {
			continue
		}
		seenPath[cp] = true
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		u, ok, _ := ParseCodex(f)
		f.Close()
		if ok {
			add(u)
		}
	}

	out := make([]shared.Usage, 0, len(acc))
	for _, u := range acc {
		out = append(out, *u)
	}
	return out
}
