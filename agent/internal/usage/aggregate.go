package usage

import (
	"os"

	"agentmon/shared"
)

// Aggregate sums usage across all of an attempt's sources into one entry per
// (provider,model). Claude rows are deduped by message.id GLOBALLY (rows repeat,
// and the same file may be enumerated twice); Codex rollouts are distinct
// sessions summed by their cumulative totals.
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

	seen := map[string]bool{} // global Claude message.id dedup
	for _, p := range s.Claude {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		rows, _ := ParseClaude(f)
		f.Close()
		for _, r := range rows {
			if r.ID != "" && seen[r.ID] {
				continue
			}
			if r.ID != "" {
				seen[r.ID] = true
			}
			add(r)
		}
	}
	for _, p := range s.Codex {
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
