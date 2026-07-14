// Package orchestrator is the hub-side epic pipeline brain: state machine,
// scheduler, merge gate, GitHub sync, and the run loop.
package orchestrator

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var ErrNoVerdict = errors.New("orchestrator: no verdict block")

type VerdictFindings struct {
	Found      int `yaml:"found"`
	Resolved   int `yaml:"resolved"`
	Unresolved int `yaml:"unresolved"`
}

type VerdictTests struct {
	Passed int `yaml:"passed"`
	Failed int `yaml:"failed"`
}

// VerdictRequirement is the runner's self-reported result for one platform
// requirement, keyed by the project Requirement's stable ID (epic-01's slug).
// Status is met | unmet | uncertain; Via records how it was certified —
// cmd (a check-command exit code) or review (a reviewer's judgment) — and is
// carried for humans only: the gate trusts a `met` regardless of Via (v1 trust
// model: PR body editable only by owner + runners). ParseVerdict validates all
// three fields, so anything reaching the gate is in-domain and dup-free.
type VerdictRequirement struct {
	ID     string `yaml:"id"`
	Status string `yaml:"status"`
	Via    string `yaml:"via"`
}

// Verdict is the runner's structured self-report, embedded as the last
// ```yaml block of the PR body. The gate treats it as data, not argument.
type Verdict struct {
	Schema           string               `yaml:"agentmon-verdict"`
	Epic             int                  `yaml:"epic"`
	Reviews          []string             `yaml:"reviews"`
	Findings         VerdictFindings      `yaml:"findings"`
	Unresolved       []string             `yaml:"unresolved"`
	Tests            VerdictTests         `yaml:"tests"`
	Requirements     []VerdictRequirement `yaml:"requirements"`
	Uncertain        bool                 `yaml:"uncertain"`
	LearningsUpdated bool                 `yaml:"learnings_updated"`
}

var fencedYAML = regexp.MustCompile("(?s)```(?:yaml|yml)\\s*\\n(.*?)```")

// ParseVerdict extracts the LAST fenced yaml block containing an
// agentmon-verdict key. Returns ErrNoVerdict when absent; a YAML error when
// the block exists but is malformed (the gate escalates on both).
func ParseVerdict(prBody string) (*Verdict, error) {
	matches := fencedYAML.FindAllStringSubmatch(prBody, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		block := matches[i][1]
		if !strings.Contains(block, "agentmon-verdict") {
			continue
		}
		// Decode exactly ONE YAML document. yaml.Unmarshal reads only the first
		// document and silently drops any that follow a `---` separator, which
		// would let a second document smuggle contradictory requirement data
		// (met for an id in doc 1, unmet for the same id in doc 2) past the
		// validation below — the same ambiguity the duplicate-id check rejects.
		// A verdict is one document, so require the block to end after it.
		dec := yaml.NewDecoder(strings.NewReader(block))
		var v Verdict
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		if err := dec.Decode(new(struct{})); !errors.Is(err, io.EOF) {
			if err == nil {
				return nil, fmt.Errorf("orchestrator: verdict block has multiple YAML documents")
			}
			return nil, err
		}
		// Fail closed on anything that isn't a well-formed v1 verdict: a block
		// that merely mentions the key (comment, prose) unmarshals to zero
		// values — Schema "" — and must not read as a clean self-report.
		if v.Schema != "v1" {
			return nil, fmt.Errorf("orchestrator: unknown verdict schema %q", v.Schema)
		}
		if v.Findings.Found < 0 || v.Findings.Resolved < 0 || v.Findings.Unresolved < 0 ||
			v.Tests.Passed < 0 || v.Tests.Failed < 0 {
			return nil, fmt.Errorf("orchestrator: negative counts in verdict")
		}
		// Requirements are validated like the rest of the verdict: fail closed on
		// anything out of the v1 domain so a malformed self-report never reads as
		// clean. Duplicate ids are rejected because a last-write-wins map in the
		// gate would let a contradictory pair (e.g. unmet then met) merge — the
		// gate must escalate on ambiguity, not resolve it. Status/via enums mirror
		// the project-side Requirement contract from epic-01.
		seen := make(map[string]bool, len(v.Requirements))
		for _, r := range v.Requirements {
			if r.ID == "" {
				return nil, fmt.Errorf("orchestrator: verdict requirement with empty id")
			}
			if seen[r.ID] {
				return nil, fmt.Errorf("orchestrator: duplicate verdict requirement id %q", r.ID)
			}
			seen[r.ID] = true
			switch r.Status {
			case "met", "unmet", "uncertain":
			default:
				return nil, fmt.Errorf("orchestrator: invalid requirement status %q for id %q", r.Status, r.ID)
			}
			switch r.Via {
			case "cmd", "review":
			default:
				return nil, fmt.Errorf("orchestrator: invalid requirement via %q for id %q", r.Via, r.ID)
			}
		}
		return &v, nil
	}
	return nil, ErrNoVerdict
}
