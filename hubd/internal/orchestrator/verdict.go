// Package orchestrator is the hub-side epic pipeline brain: state machine,
// scheduler, merge gate, GitHub sync, and the run loop.
package orchestrator

import (
	"errors"
	"fmt"
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

// Verdict is the runner's structured self-report, embedded as the last
// ```yaml block of the PR body. The gate treats it as data, not argument.
type Verdict struct {
	Schema           string          `yaml:"agentmon-verdict"`
	Epic             int             `yaml:"epic"`
	Reviews          []string        `yaml:"reviews"`
	Findings         VerdictFindings `yaml:"findings"`
	Unresolved       []string        `yaml:"unresolved"`
	Tests            VerdictTests    `yaml:"tests"`
	Uncertain        bool            `yaml:"uncertain"`
	LearningsUpdated bool            `yaml:"learnings_updated"`
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
		var v Verdict
		if err := yaml.Unmarshal([]byte(block), &v); err != nil {
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
		return &v, nil
	}
	return nil, ErrNoVerdict
}
