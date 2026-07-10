package orchestrator

import (
	"errors"
	"testing"
)

const goodBody = "Implements #15.\n\n```yaml\n" +
	"agentmon-verdict: v1\n" +
	"epic: 15\n" +
	"reviews: [specialist, simplifier, deep-scan, codex]\n" +
	"findings: { found: 9, resolved: 7, unresolved: 2 }\n" +
	"unresolved:\n  - \"Deletion cascade\"\n  - \"Retention default\"\n" +
	"tests: { passed: 47, failed: 0 }\n" +
	"uncertain: true\n" +
	"learnings_updated: true\n" +
	"```\n"

func TestParseVerdict(t *testing.T) {
	v, err := ParseVerdict(goodBody)
	if err != nil {
		t.Fatal(err)
	}
	if v.Schema != "v1" || v.Epic != 15 || len(v.Reviews) != 4 ||
		v.Findings.Unresolved != 2 || v.Tests.Passed != 47 || !v.Uncertain || !v.LearningsUpdated {
		t.Fatalf("got %+v", v)
	}
	if len(v.Unresolved) != 2 || v.Unresolved[0] != "Deletion cascade" {
		t.Fatalf("unresolved = %v", v.Unresolved)
	}
}

func TestParseVerdictPicksLastBlock(t *testing.T) {
	body := "```yaml\nagentmon-verdict: v1\nepic: 1\nuncertain: true\n```\n\nrevised:\n\n" +
		"```yaml\nagentmon-verdict: v1\nepic: 1\nuncertain: false\n```\n"
	v, err := ParseVerdict(body)
	if err != nil || v.Uncertain {
		t.Fatalf("want last block (uncertain=false), got %+v err=%v", v, err)
	}
}

func TestParseVerdictMissing(t *testing.T) {
	if _, err := ParseVerdict("no block here\n```yaml\nother: doc\n```"); !errors.Is(err, ErrNoVerdict) {
		t.Fatalf("want ErrNoVerdict, got %v", err)
	}
}

func TestParseVerdictMalformed(t *testing.T) {
	if _, err := ParseVerdict("```yaml\nagentmon-verdict: v1\nepic: [broken\n```"); err == nil || errors.Is(err, ErrNoVerdict) {
		t.Fatalf("malformed block must be a distinct error, got %v", err)
	}
}
