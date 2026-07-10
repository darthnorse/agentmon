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

func TestParseVerdictRejectsWrongOrMissingSchema(t *testing.T) {
	// A block that merely MENTIONS the key (comment/prose) unmarshals to a
	// zero Verdict — Schema "" — and must fail closed, not read as clean.
	if _, err := ParseVerdict("```yaml\n# see agentmon-verdict format docs\nother: thing\n```"); err == nil {
		t.Fatal("comment-only mention must not parse as a verdict")
	}
	if _, err := ParseVerdict("```yaml\nagentmon-verdict: v2\nepic: 1\n```"); err == nil {
		t.Fatal("unknown schema version must fail closed")
	}
}

func TestParseVerdictRejectsNegativeCounts(t *testing.T) {
	body := "```yaml\nagentmon-verdict: v1\nepic: 1\n" +
		"findings: {found: 1, resolved: 2, unresolved: -1}\ntests: {passed: 1, failed: 0}\n```"
	if _, err := ParseVerdict(body); err == nil {
		t.Fatal("negative counts must fail closed")
	}
}
