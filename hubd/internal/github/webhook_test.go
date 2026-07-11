package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func sign(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func TestVerifySignature(t *testing.T) {
	body := []byte(`{"zen":"x"}`)
	if !VerifySignature("s3cret", body, sign("s3cret", body)) {
		t.Fatal("valid signature rejected")
	}
	if VerifySignature("s3cret", body, sign("wrong", body)) {
		t.Fatal("bad signature accepted")
	}
	if VerifySignature("", body, sign("", body)) {
		t.Fatal("empty secret must always fail")
	}
	if VerifySignature("s3cret", body, "") {
		t.Fatal("missing header must fail")
	}
}

func TestParseIssuesEvent(t *testing.T) {
	ev, err := ParseEvent("issues", []byte(`{
	  "action": "labeled",
	  "repository": {"full_name": "o/r"},
	  "issue": {"number": 15, "title": "GDPR", "body": "Blocked by #13", "state": "open",
	            "labels": [{"name":"agentmon:epic"}], "updated_at": "2026-07-10T10:00:00Z"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Kind != "issues" || ev.Action != "labeled" || ev.Repo != "o/r" {
		t.Fatalf("got %+v", ev)
	}
	if ev.Issue == nil || ev.Issue.Number != 15 || ev.Issue.Labels[0] != "agentmon:epic" {
		t.Fatalf("issue = %+v", ev.Issue)
	}
}

func TestParsePullRequestEvent(t *testing.T) {
	ev, err := ParseEvent("pull_request", []byte(`{
	  "action": "closed",
	  "repository": {"full_name": "o/r"},
	  "pull_request": {"number": 58, "merged": true}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if ev.PRNumber != 58 || !ev.PRMerged || ev.Action != "closed" {
		t.Fatalf("got %+v", ev)
	}
}

func TestParseUnknownKind(t *testing.T) {
	ev, err := ParseEvent("workflow_run", []byte(`{"repository":{"full_name":"o/r"}}`))
	if err != nil || ev.Kind != "workflow_run" || ev.Repo != "o/r" {
		t.Fatalf("ev=%+v err=%v", ev, err)
	}
}
