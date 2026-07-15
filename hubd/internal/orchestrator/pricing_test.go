package orchestrator

import (
	"math"
	"testing"
)

func TestCostUSDKnownModelExact(t *testing.T) {
	// claude-opus-4-8: In=5, Out=25, CacheRead=0.50, CacheWrite=6.25 $/Mtok
	// (1e6*5 + 1e6*25 + 1e6*0.5 + 1e6*6.25) / 1e6 = 5+25+0.5+6.25 = 36.75
	c := CostUSD(1_000_000, 1_000_000, 1_000_000, 1_000_000, "claude-opus-4-8")
	if c == nil || math.Abs(*c-36.75) > 1e-9 {
		t.Fatalf("got %v, expected 36.75", c)
	}
}

func TestCostUSDSuffixNormalization(t *testing.T) {
	// claude-opus-4-8[1m] should normalize to claude-opus-4-8
	// (1e6*5 + 0 + 0 + 0) / 1e6 = 5.0
	c := CostUSD(1_000_000, 0, 0, 0, "claude-opus-4-8[1m]")
	if c == nil || math.Abs(*c-5.0) > 1e-9 {
		t.Fatalf("got %v, expected 5.0", c)
	}
}

func TestCostUSDUnknownModel(t *testing.T) {
	// Unknown model should return nil
	c := CostUSD(1, 1, 1, 1, "who-dis")
	if c != nil {
		t.Fatalf("got %v, expected nil for unknown model", c)
	}
}

func TestCostUSDCodex(t *testing.T) {
	// gpt-5.6-sol: In=5, Out=30, CacheRead=0.50, CacheWrite=0 $/Mtok
	// (1e6*5 + 1e6*30 + 1e6*0.5 + 0) / 1e6 = 5+30+0.5 = 35.5
	c := CostUSD(1_000_000, 1_000_000, 1_000_000, 0, "gpt-5.6-sol")
	if c == nil || math.Abs(*c-35.5) > 1e-9 {
		t.Fatalf("got %v, expected 35.5", c)
	}
}
