package api

import (
	"fmt"
	"regexp"
	"strings"

	"agentmon/hubd/internal/db"
)

// nonSlugRe collapses every run of non-[a-z0-9] characters to a single dash.
var nonSlugRe = regexp.MustCompile(`[^a-z0-9]+`)

// slugify derives a stable lowercase-kebab id: lowercase, non-alphanumeric runs →
// single dash, trimmed of leading/trailing dashes. "Always use RLS" →
// "always-use-rls". It is idempotent on an already-derived slug.
func slugify(s string) string {
	s = nonSlugRe.ReplaceAllString(strings.ToLower(s), "-")
	return strings.Trim(s, "-")
}

// normalizeRequirements shapes + validates author input into the stored form:
//   - trims id/text/check_cmd,
//   - drops any row with blank text (text is the review lens — meaningless without it),
//   - resolves the id by slugifying the supplied id, falling back to the text
//     when the supplied id is empty OR unsluggable. Slugifying the supplied id
//     enforces the lowercase-kebab invariant while keeping it STABLE across later
//     text edits (it is derived from the id, never from edited text; slugify is
//     idempotent on an existing slug),
//   - drops the row only when the text too has no slug-able characters,
//   - rejects duplicate resolved ids: the id is the join key the epic-02 gate
//     matches on, so two rows sharing one id would make a single verdict entry
//     ambiguous. Fail closed here — mirroring the handler's provider/max_parallel
//     400s — rather than store an unenforceable set.
func normalizeRequirements(in []db.Requirement) ([]db.Requirement, error) {
	out := make([]db.Requirement, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, r := range in {
		r.ID = strings.TrimSpace(r.ID)
		r.Text = strings.TrimSpace(r.Text)
		r.CheckCmd = strings.TrimSpace(r.CheckCmd)
		if r.Text == "" {
			continue
		}
		r.ID = slugify(r.ID)
		if r.ID == "" {
			r.ID = slugify(r.Text)
		}
		if r.ID == "" {
			continue
		}
		if seen[r.ID] {
			return nil, fmt.Errorf("duplicate requirement id %q", r.ID)
		}
		seen[r.ID] = true
		out = append(out, r)
	}
	return out, nil
}
