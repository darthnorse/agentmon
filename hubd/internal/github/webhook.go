package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// VerifySignature checks X-Hub-Signature-256 over the raw body. Fails closed:
// no secret configured or no header ⇒ reject.
func VerifySignature(secret string, body []byte, sigHeader string) bool {
	if secret == "" || !strings.HasPrefix(sigHeader, "sha256=") {
		return false
	}
	want, err := hex.DecodeString(strings.TrimPrefix(sigHeader, "sha256="))
	if err != nil {
		return false
	}
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return hmac.Equal(m.Sum(nil), want)
}

// Event is the orchestrator-relevant projection of one webhook delivery.
type Event struct {
	Kind     string
	Action   string
	Repo     string
	Issue    *Issue
	PRNumber int
	PRMerged bool
}

func ParseEvent(kind string, body []byte) (Event, error) {
	var w struct {
		Action     string `json:"action"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
		Issue       *wireIssue `json:"issue"`
		PullRequest *struct {
			Number int  `json:"number"`
			Merged bool `json:"merged"`
		} `json:"pull_request"`
	}
	if err := json.Unmarshal(body, &w); err != nil {
		return Event{}, err
	}
	ev := Event{Kind: kind, Action: w.Action, Repo: w.Repository.FullName}
	if w.Issue != nil {
		is := w.Issue.issue()
		ev.Issue = &is
	}
	if w.PullRequest != nil {
		ev.PRNumber = w.PullRequest.Number
		ev.PRMerged = w.PullRequest.Merged
	}
	return ev, nil
}
