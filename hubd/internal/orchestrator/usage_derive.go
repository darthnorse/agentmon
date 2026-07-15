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

// AddTokens sums two TokenTotals, recomputing Total from the summed parts
// rather than adding the Total fields (same never-independently-derived
// invariant as the unexported add). Exported for the API layer's project-wide
// usage aggregation (Task 14), which folds TokenTotals across many epics'
// derived breakdowns from outside this package.
func AddTokens(a, b TokenTotals) TokenTotals {
	return a.add(b)
}

func (t TokenTotals) sub(o TokenTotals) TokenTotals {
	return newTokenTotals(t.Input-o.Input, t.Output-o.Output, t.CacheRead-o.CacheRead, t.CacheWrite-o.CacheWrite)
}

// clampNonNeg floors each bucket at 0, recomputing Total from the floored
// parts (never independently derived, same invariant as every other
// TokenTotals constructor). Used on a per-interval delta: if a later
// boundary reports a LOWER cumulative than an earlier one for the same
// (provider,model) — a source dropping out mid-run, or any other
// non-monotonicity — the naive delta goes negative. A decreasing cumulative
// must contribute 0 to that interval, never subtract from it; subtracting
// would produce negative stage tokens and, downstream, a negative CostUSD
// polluting by_stage/by_model/totals.
func (t TokenTotals) clampNonNeg() TokenTotals {
	clamp := func(v int64) int64 {
		if v < 0 {
			return 0
		}
		return v
	}
	return newTokenTotals(clamp(t.Input), clamp(t.Output), clamp(t.CacheRead), clamp(t.CacheWrite))
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

// ModelKey identifies a (provider, model) bucket used to fold token totals
// while deriving usage. Exported so the API layer's project-wide aggregation
// (which folds usage across many epics from outside this package) can build
// the same map keys ModelUsageList/AggregateCost expect, instead of keeping
// its own near-identical copy.
type ModelKey struct {
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
	cum   map[ModelKey]TokenTotals
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

	attempts := make([]UsageAttempt, 0, len(attemptNums))
	epicTokens := TokenTotals{}
	epicModelTokens := map[ModelKey]TokenTotals{}

	for _, n := range attemptNums {
		// The "current" attempt is the one matching e.Attempt — NOT necessarily
		// max(attemptNums). e.Attempt is bumped at spawn time, before the new
		// attempt has reported any usage rows; in that window the highest
		// attempt number present in the rows still belongs to the just-finished
		// PRIOR attempt, which must be labeled "retried", not the live epic's
		// Outcome/IsLowerBound.
		att, finalCum := deriveAttempt(byAttempt[n], n, n == e.Attempt, e)
		attempts = append(attempts, att)
		epicTokens = epicTokens.add(att.Tokens)
		for k, v := range finalCum {
			epicModelTokens[k] = epicModelTokens[k].add(v)
		}
	}

	epicModels := ModelUsageList(epicModelTokens)

	return EpicUsage{
		Tokens:     epicTokens,
		Cost:       AggregateCost(epicModels),
		DurationMs: epicDurationMs(e, rows),
		ByModel:    epicModels,
		Attempts:   attempts,
	}
}

// epicDurationMs is the epic's wall-clock span, best-effort. Prefers
// MergedAt−StartedAt when both are present and parse. A running/escalated
// epic has no MergedAt yet — without a fallback the inline rollup would
// always show 0m while the drawer (which derives duration from the same
// boundaries) shows real elapsed time, a visible contradiction on the same
// card. So when MergedAt is empty or fails to parse, fall back to the
// boundary span across the epic's own usage rows: max(captured_at) −
// min(captured_at), best-effort parsed, 0 on failure/absence.
func epicDurationMs(e db.Epic, rows []db.UsageRow) int64 {
	if e.StartedAt != "" && e.MergedAt != "" {
		if start, err := time.Parse(time.RFC3339, e.StartedAt); err == nil {
			if end, err := time.Parse(time.RFC3339, e.MergedAt); err == nil {
				if d := end.Sub(start).Milliseconds(); d > 0 {
					return d
				}
				return 0
			}
		}
	}
	return boundarySpanMs(rows)
}

// boundarySpanMs is the best-effort wall-clock span (max − min) across a set
// of usage rows' captured_at timestamps. 0 if fewer than one timestamp
// parses.
func boundarySpanMs(rows []db.UsageRow) int64 {
	var min, max time.Time
	have := false
	for _, r := range rows {
		t, err := time.Parse(time.RFC3339, r.CapturedAt)
		if err != nil {
			continue
		}
		if !have {
			min, max = t, t
			have = true
			continue
		}
		if t.Before(min) {
			min = t
		}
		if t.After(max) {
			max = t
		}
	}
	if !have {
		return 0
	}
	if d := max.Sub(min).Milliseconds(); d > 0 {
		return d
	}
	return 0
}

// deriveAttempt derives one attempt's UsageAttempt, plus the final (last
// boundary) cumulative per (provider,model) — the epic-level rollup needs
// that same map to sum across attempts. isCurrent marks the attempt whose
// number equals e.Attempt (the live attempt, which may still be running or
// may not have reported any usage rows yet); every other attempt is a
// prior/superseded one.
func deriveAttempt(rows []db.UsageRow, attemptNum int, isCurrent bool, e db.Epic) (UsageAttempt, map[ModelKey]TokenTotals) {
	boundaries := buildBoundaries(rows)
	sortBoundaries(boundaries)

	seen := map[ModelKey]bool{}
	for _, b := range boundaries {
		for k := range b.cum {
			seen[k] = true
		}
	}

	cum := map[ModelKey]TokenTotals{} // carried-forward cumulative, keyed by model; a missing key reads as the zero value (0), which is exactly the "never seen yet" baseline.
	stageOrder := []string{}
	stageSeen := map[string]bool{}
	stageTokens := map[string]map[ModelKey]TokenTotals{}
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
			stageTokens[stage] = map[ModelKey]TokenTotals{}
		}

		for k := range seen {
			target := cum[k] // default: carry forward unchanged (delta 0) when absent at this boundary
			if v, ok := b.cum[k]; ok {
				target = v
			}
			delta := target.sub(cum[k]).clampNonNeg()
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
		models := ModelUsageList(stageTokens[stage])
		t := TokenTotals{}
		for _, m := range models {
			t = t.add(m.Tokens)
		}
		stages = append(stages, UsageStage{
			Stage:      stage,
			DurationMs: stageDurationMs[stage],
			Tokens:     t,
			Cost:       AggregateCost(models),
			ByModel:    models,
		})
	}

	finalModels := ModelUsageList(cum)
	attemptTokens := TokenTotals{}
	for _, m := range finalModels {
		attemptTokens = attemptTokens.add(m.Tokens)
	}

	outcome := "retried"
	lowerBound := false
	if isCurrent {
		outcome = e.Stage
		lowerBound = !isTerminalEpicStage(e.Stage)
	}

	return UsageAttempt{
		Attempt:      attemptNum,
		Outcome:      outcome,
		DurationMs:   attemptDurationMs,
		Tokens:       attemptTokens,
		Cost:         AggregateCost(finalModels),
		IsLowerBound: lowerBound,
		Stages:       stages,
	}, cum
}

// boundaryKey identifies one runner report within an attempt: the
// (captured_at, stage) pair. Keying on captured_at alone would collapse two
// DIFFERENT-stage reports landed in the same second (a same-second reap
// alongside a stage report, or two rapid reports) into one boundary whose
// stage is whichever row is iterated first and whose same-(provider,model)
// cumulatives silently overwrite each other. Seconds precision is
// intentional — do NOT widen captured_at to nanoseconds to disambiguate:
// nanosecond-width timestamps are variable-length and break the lexicographic/
// SQL ordering the ledger and its ORDER BY rely on. Two reports that
// genuinely share both captured_at AND stage are covered by UpsertUsage's
// UNIQUE key (idempotent redelivery), not a real collision here.
type boundaryKey struct {
	capturedAt string
	stage      string
}

// buildBoundaries groups one attempt's rows by (captured_at, stage) — see
// boundaryKey — each contributing its own (provider,model) cumulative
// snapshot. The boundary sequence preserves row iteration order: callers
// must pass rows already ordered captured_at, then insertion (rowid) —
// exactly what ListEpicUsage/ListProjectUsage now return — so that two
// same-second different-stage reports become two distinct boundaries in the
// order they actually landed (a reap inserted last after its stage's own
// report sorts last within their shared second).
func buildBoundaries(rows []db.UsageRow) []*usageBoundary {
	idx := map[boundaryKey]*usageBoundary{}
	var boundaries []*usageBoundary
	for _, r := range rows {
		k := boundaryKey{capturedAt: r.CapturedAt, stage: r.Stage}
		b, ok := idx[k]
		if !ok {
			b = &usageBoundary{raw: r.CapturedAt, stage: r.Stage, cum: map[ModelKey]TokenTotals{}}
			if t, err := time.Parse(time.RFC3339, r.CapturedAt); err == nil {
				b.ts, b.tsOK = t, true
			}
			idx[k] = b
			boundaries = append(boundaries, b)
		}
		// Within one (captured_at, stage) boundary, UpsertUsage's UNIQUE key
		// guarantees at most one row per (provider,model) — no overwrite
		// concern here, just a plain assignment.
		b.cum[ModelKey{Provider: r.Provider, Model: r.Model}] = newTokenTotals(r.Input, r.Output, r.CacheRead, r.CacheWrite)
	}
	return boundaries
}

// sortBoundaries orders boundaries ascending by captured_at. Timestamps are
// RFC3339 and parsed for a correct chronological sort; if either side fails
// to parse (malformed data — never expected from an agent-stamped report,
// but this must never panic), it falls back to a raw string compare. Two
// boundaries can now legitimately share the same captured_at (different
// stages, same second — see boundaryKey): in that case raw is identical too,
// so the comparator reports neither as "less", and sort.SliceStable leaves
// them in their original (row-iteration = insertion) order rather than
// reordering them arbitrarily.
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

// ModelUsageList turns a (provider,model) → cumulative-delta map into a
// sorted, priced breakdown. Zero-total entries are dropped: a (provider,model)
// that never actually contributed to this scope (e.g. a global-union member
// carried forward at 0 through an interval before it first appeared) is not
// "present" there, and a zero-token model can never affect cost regardless of
// whether its rate is known.
func ModelUsageList(byModel map[ModelKey]TokenTotals) []ModelUsage {
	keys := make([]ModelKey, 0, len(byModel))
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

// AggregateCost sums a scope's already-priced model breakdown: nil if the
// scope has no priced models (no data), or if ANY model in it is unpriced
// (unknown to the rate card) — a partial total would understate real cost
// and render as a misleadingly low dollar figure, so this fails closed to
// "$—" rather than quietly dropping the unknown model's share.
func AggregateCost(models []ModelUsage) *float64 {
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
