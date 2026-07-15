package orchestrator

import (
	"sort"
	"time"

	"agentmon/hubd/internal/db"
	"agentmon/shared"
)

// TokenTotals is four raw token buckets plus their sum. Total is always
// Input+Output+CacheRead+CacheWrite, never independently derived, so it can
// never drift from its parts.
type TokenTotals struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
	Total      int64 `json:"total"`
}

func newTokenTotals(input, output, cacheRead, cacheWrite int64) TokenTotals {
	return TokenTotals{
		Input: input, Output: output, CacheRead: cacheRead, CacheWrite: cacheWrite,
		Total: input + output + cacheRead + cacheWrite,
	}
}

func (t TokenTotals) add(o TokenTotals) TokenTotals {
	return newTokenTotals(t.Input+o.Input, t.Output+o.Output, t.CacheRead+o.CacheRead, t.CacheWrite+o.CacheWrite)
}

func (t TokenTotals) sub(o TokenTotals) TokenTotals {
	return newTokenTotals(t.Input-o.Input, t.Output-o.Output, t.CacheRead-o.CacheRead, t.CacheWrite-o.CacheWrite)
}

// ModelUsage is one (provider, model)'s token/cost contribution within some
// scope (a stage, or the whole epic).
type ModelUsage struct {
	Provider string      `json:"provider"`
	Model    string      `json:"model"`
	Tokens   TokenTotals `json:"tokens"`
	Cost     *float64    `json:"cost"`
}

// UsageStage is one pipeline stage's contribution within one attempt,
// aggregated across every interval attributed to it (a stage a runner
// revisits, e.g. a reviewing→implementing→reviewing checkpoint loop, sums
// across all its occurrences into a single entry).
type UsageStage struct {
	Stage      string       `json:"stage"`
	DurationMs int64        `json:"duration_ms"`
	Tokens     TokenTotals  `json:"tokens"`
	Cost       *float64     `json:"cost"`
	ByModel    []ModelUsage `json:"by_model"`
}

// UsageAttempt is one epic attempt's full per-stage breakdown.
type UsageAttempt struct {
	Attempt      int          `json:"attempt"`
	Outcome      string       `json:"outcome"`
	DurationMs   int64        `json:"duration_ms"`
	Tokens       TokenTotals  `json:"tokens"`
	Cost         *float64     `json:"cost"`
	IsLowerBound bool         `json:"is_lower_bound"`
	Stages       []UsageStage `json:"stages"`
}

// EpicUsage is the full derived usage breakdown for one epic, across every
// attempt it has been run.
type EpicUsage struct {
	Tokens     TokenTotals    `json:"tokens"`
	Cost       *float64       `json:"cost"`
	DurationMs int64          `json:"duration_ms"`
	ByModel    []ModelUsage   `json:"by_model"`
	Attempts   []UsageAttempt `json:"attempts"`
}

type modelKey struct {
	Provider string
	Model    string
}

// usageBoundary is one distinct captured_at value within an attempt: every
// row sharing that timestamp came from the same runner report, so they share
// one Stage and contribute their own (provider,model) cumulative snapshot.
type usageBoundary struct {
	raw   string
	ts    time.Time
	tsOK  bool
	stage string
	cum   map[modelKey]TokenTotals
}

// DeriveEpicUsage turns the raw cumulative token snapshots recorded for one
// epic into the full per-attempt → per-stage → per-model breakdown.
//
// ATTRIBUTION RULE: a runner reports a stage when ENTERING it (e.g. it
// reports "planning" before doing any planning work), so the work done
// between two consecutive reports belongs to the stage named by the EARLIER
// report — the interval's *starting* boundary, not its ending one. Per
// (provider,model), boundaries are ordered by captured_at and a provider/model
// absent at a boundary carries forward its last known cumulative value (delta
// 0 for that interval) rather than resetting to 0; the very first time a
// (provider,model) is ever seen, its baseline is 0 (so its first cumulative
// snapshot becomes real work, attributed like any other interval). The last
// boundary's own stage is therefore inert — no interval starts there — which
// is intentional: the reap/terminal snapshot's stage label never matters.
func DeriveEpicUsage(rows []db.UsageRow, e db.Epic) EpicUsage {
	byAttempt := map[int][]db.UsageRow{}
	var attemptNums []int
	for _, r := range rows {
		if _, ok := byAttempt[r.Attempt]; !ok {
			attemptNums = append(attemptNums, r.Attempt)
		}
		byAttempt[r.Attempt] = append(byAttempt[r.Attempt], r)
	}
	sort.Ints(attemptNums)

	maxAttempt := 0
	for _, n := range attemptNums {
		if n > maxAttempt {
			maxAttempt = n
		}
	}

	attempts := make([]UsageAttempt, 0, len(attemptNums))
	epicTokens := TokenTotals{}
	epicModelTokens := map[modelKey]TokenTotals{}

	for _, n := range attemptNums {
		att, finalCum := deriveAttempt(byAttempt[n], n, n == maxAttempt, e)
		attempts = append(attempts, att)
		epicTokens = epicTokens.add(att.Tokens)
		for k, v := range finalCum {
			epicModelTokens[k] = epicModelTokens[k].add(v)
		}
	}

	epicModels := modelUsageList(epicModelTokens)

	return EpicUsage{
		Tokens:     epicTokens,
		Cost:       aggregateCost(epicModels),
		DurationMs: epicDurationMs(e),
		ByModel:    epicModels,
		Attempts:   attempts,
	}
}

// epicDurationMs is the epic's wall-clock span, best-effort: 0 unless both
// timestamps are present and parse.
func epicDurationMs(e db.Epic) int64 {
	if e.StartedAt == "" || e.MergedAt == "" {
		return 0
	}
	start, err := time.Parse(time.RFC3339, e.StartedAt)
	if err != nil {
		return 0
	}
	end, err := time.Parse(time.RFC3339, e.MergedAt)
	if err != nil {
		return 0
	}
	if d := end.Sub(start).Milliseconds(); d > 0 {
		return d
	}
	return 0
}

// deriveAttempt derives one attempt's UsageAttempt, plus the final (last
// boundary) cumulative per (provider,model) — the epic-level rollup needs
// that same map to sum across attempts.
func deriveAttempt(rows []db.UsageRow, attemptNum int, isLast bool, e db.Epic) (UsageAttempt, map[modelKey]TokenTotals) {
	boundaries := buildBoundaries(rows)
	sortBoundaries(boundaries)

	seen := map[modelKey]bool{}
	for _, b := range boundaries {
		for k := range b.cum {
			seen[k] = true
		}
	}

	cum := map[modelKey]TokenTotals{} // carried-forward cumulative, keyed by model; a missing key reads as the zero value (0), which is exactly the "never seen yet" baseline.
	stageOrder := []string{}
	stageSeen := map[string]bool{}
	stageTokens := map[string]map[modelKey]TokenTotals{}
	stageDurationMs := map[string]int64{}
	var attemptDurationMs int64

	for i, b := range boundaries {
		// The interval ENDING at boundary i is attributed to the stage
		// ENTERED at its starting boundary: boundaries[i-1] for i>=1, or
		// boundaries[0] itself for the leading interval (i==0, session
		// start → B0, baseline 0).
		startIdx := i - 1
		if startIdx < 0 {
			startIdx = 0
		}
		stage := boundaries[startIdx].stage

		if !stageSeen[stage] {
			stageSeen[stage] = true
			stageOrder = append(stageOrder, stage)
			stageTokens[stage] = map[modelKey]TokenTotals{}
		}

		for k := range seen {
			target := cum[k] // default: carry forward unchanged (delta 0) when absent at this boundary
			if v, ok := b.cum[k]; ok {
				target = v
			}
			delta := target.sub(cum[k])
			stageTokens[stage][k] = stageTokens[stage][k].add(delta)
		}

		if i > 0 {
			prev := boundaries[i-1]
			var span int64
			if b.tsOK && prev.tsOK {
				if s := b.ts.Sub(prev.ts).Milliseconds(); s > 0 {
					span = s
				}
			}
			stageDurationMs[stage] += span
			attemptDurationMs += span
		}

		for k, v := range b.cum {
			cum[k] = v
		}
	}

	stages := make([]UsageStage, 0, len(stageOrder))
	for _, stage := range stageOrder {
		models := modelUsageList(stageTokens[stage])
		t := TokenTotals{}
		for _, m := range models {
			t = t.add(m.Tokens)
		}
		stages = append(stages, UsageStage{
			Stage:      stage,
			DurationMs: stageDurationMs[stage],
			Tokens:     t,
			Cost:       aggregateCost(models),
			ByModel:    models,
		})
	}

	finalModels := modelUsageList(cum)
	attemptTokens := TokenTotals{}
	for _, m := range finalModels {
		attemptTokens = attemptTokens.add(m.Tokens)
	}

	outcome := "retried"
	lowerBound := false
	if isLast {
		outcome = e.Stage
		lowerBound = !isTerminalEpicStage(e.Stage)
	}

	return UsageAttempt{
		Attempt:      attemptNum,
		Outcome:      outcome,
		DurationMs:   attemptDurationMs,
		Tokens:       attemptTokens,
		Cost:         aggregateCost(finalModels),
		IsLowerBound: lowerBound,
		Stages:       stages,
	}, cum
}

// buildBoundaries groups one attempt's rows by their exact captured_at value:
// every row sharing a captured_at came from one runner report (same Stage),
// each contributing its own (provider,model) cumulative snapshot.
func buildBoundaries(rows []db.UsageRow) []*usageBoundary {
	idx := map[string]*usageBoundary{}
	var boundaries []*usageBoundary
	for _, r := range rows {
		b, ok := idx[r.CapturedAt]
		if !ok {
			b = &usageBoundary{raw: r.CapturedAt, stage: r.Stage, cum: map[modelKey]TokenTotals{}}
			if t, err := time.Parse(time.RFC3339, r.CapturedAt); err == nil {
				b.ts, b.tsOK = t, true
			}
			idx[r.CapturedAt] = b
			boundaries = append(boundaries, b)
		}
		b.cum[modelKey{Provider: r.Provider, Model: r.Model}] = newTokenTotals(r.Input, r.Output, r.CacheRead, r.CacheWrite)
	}
	return boundaries
}

// sortBoundaries orders boundaries ascending by captured_at. Timestamps are
// RFC3339 and parsed for a correct chronological sort; if either side fails
// to parse (malformed data — never expected from an agent-stamped report,
// but this must never panic), it falls back to a raw string compare so the
// order stays deterministic.
func sortBoundaries(boundaries []*usageBoundary) {
	sort.SliceStable(boundaries, func(i, j int) bool {
		bi, bj := boundaries[i], boundaries[j]
		if bi.tsOK && bj.tsOK && !bi.ts.Equal(bj.ts) {
			return bi.ts.Before(bj.ts)
		}
		return bi.raw < bj.raw
	})
}

// isTerminalEpicStage reports whether stage is one of the epic's terminal
// stages (merged/failed/canceled) — see shared.EpicStage.
func isTerminalEpicStage(stage string) bool {
	switch stage {
	case string(shared.EpicMerged), string(shared.EpicFailed), string(shared.EpicCanceled):
		return true
	default:
		return false
	}
}

// modelUsageList turns a (provider,model) → cumulative-delta map into a
// sorted, priced breakdown. Zero-total entries are dropped: a (provider,model)
// that never actually contributed to this scope (e.g. a global-union member
// carried forward at 0 through an interval before it first appeared) is not
// "present" there, and a zero-token model can never affect cost regardless of
// whether its rate is known.
func modelUsageList(byModel map[modelKey]TokenTotals) []ModelUsage {
	keys := make([]modelKey, 0, len(byModel))
	for k, t := range byModel {
		if t.Total == 0 {
			continue
		}
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Provider != keys[j].Provider {
			return keys[i].Provider < keys[j].Provider
		}
		return keys[i].Model < keys[j].Model
	})
	out := make([]ModelUsage, 0, len(keys))
	for _, k := range keys {
		t := byModel[k]
		out = append(out, ModelUsage{
			Provider: k.Provider,
			Model:    k.Model,
			Tokens:   t,
			Cost:     CostUSD(t.Input, t.Output, t.CacheRead, t.CacheWrite, k.Model),
		})
	}
	return out
}

// aggregateCost sums a scope's already-priced model breakdown: nil if the
// scope has no priced models (no data), or if ANY model in it is unpriced
// (unknown to the rate card) — a partial total would understate real cost
// and render as a misleadingly low dollar figure, so this fails closed to
// "$—" rather than quietly dropping the unknown model's share.
func aggregateCost(models []ModelUsage) *float64 {
	if len(models) == 0 {
		return nil
	}
	total := 0.0
	for _, m := range models {
		if m.Cost == nil {
			return nil
		}
		total += *m.Cost
	}
	return &total
}
