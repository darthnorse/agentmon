package api

import (
	"testing"

	"agentmon/hubd/internal/db"
)

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{
		"Always use RLS":   "always-use-rls",
		"WCAG 2.2 AA":      "wcag-2-2-aa",
		"  Trim  Me  ":     "trim-me",
		"No PII in logs!":  "no-pii-in-logs",
		"tenant_isolation": "tenant-isolation",
		"---edge---":       "edge",
		"!!!":              "",
	} {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeRequirements(t *testing.T) {
	got, err := normalizeRequirements([]db.Requirement{
		{Text: "Always use RLS"},                          // id derived from text
		{ID: "WCAG 2.2", Text: "WCAG 2.2 renamed to 2.3"}, // supplied id normalized to kebab, stable vs text
		{Text: "   "}, // blank text dropped
		{Text: "!!!"}, // unsluggable text dropped
		{Text: "  No PII in logs  ", CheckCmd: "  s.sh "}, // trimmed
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []db.Requirement{
		{ID: "always-use-rls", Text: "Always use RLS"},
		{ID: "wcag-2-2", Text: "WCAG 2.2 renamed to 2.3"},
		{ID: "no-pii-in-logs", Text: "No PII in logs", CheckCmd: "s.sh"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("requirement %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestNormalizeRequirementsRejectsDuplicateIDs(t *testing.T) {
	// Two rows resolving to the same id would make the epic-02 join ambiguous.
	if _, err := normalizeRequirements([]db.Requirement{
		{Text: "Always use RLS"},
		{ID: "always-use-rls", Text: "A different standard"},
	}); err == nil {
		t.Fatal("duplicate resolved id must error")
	}
}
