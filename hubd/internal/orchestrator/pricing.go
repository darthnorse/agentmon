package orchestrator

import "strings"

type Rate struct {
	In         float64
	Out        float64
	CacheRead  float64
	CacheWrite float64
}

var rateCard = map[string]Rate{
	"claude-opus-4-8":           {In: 5, Out: 25, CacheRead: 0.50, CacheWrite: 6.25},
	"claude-haiku-4-5-20251001": {In: 1, Out: 5, CacheRead: 0.10, CacheWrite: 1.25},
	"gpt-5.6-sol":               {In: 5, Out: 30, CacheRead: 0.50, CacheWrite: 0},
}

// CostUSD computes notional cost in USD from token buckets and model ID.
// Returns nil for an unknown model.
// The model ID is normalized by stripping any trailing bracketed suffix (e.g., "[1m]").
func CostUSD(input, output, cacheRead, cacheWrite int64, model string) *float64 {
	// Normalize model ID: strip trailing bracketed suffix
	normalizedModel := model
	if idx := strings.Index(model, "["); idx != -1 {
		normalizedModel = model[:idx]
	}

	rate, ok := rateCard[normalizedModel]
	if !ok {
		return nil
	}

	// (input*In + output*Out + cacheRead*CacheRead + cacheWrite*CacheWrite) / 1e6
	cost := (float64(input)*rate.In + float64(output)*rate.Out +
		float64(cacheRead)*rate.CacheRead + float64(cacheWrite)*rate.CacheWrite) / 1e6

	return &cost
}
