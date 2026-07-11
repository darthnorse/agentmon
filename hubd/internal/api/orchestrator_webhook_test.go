package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"agentmon/hubd/internal/github"
)

type fakeOrch struct {
	ingested []github.Event
	woke     int
}

func (f *fakeOrch) IngestWebhook(_ context.Context, ev github.Event) error {
	f.ingested = append(f.ingested, ev)
	return nil
}
func (f *fakeOrch) Wake()                                         { f.woke++ }
func (f *fakeOrch) Approve(context.Context, string, string) error { return nil }
func (f *fakeOrch) Retry(context.Context, string, string) error   { return nil }
func (f *fakeOrch) Cancel(context.Context, string, string) error  { return nil }
func (f *fakeOrch) RunIssue(context.Context, string, int) error   { return nil }
func signBody(secret string, b []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(b)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}
func TestWebhookRejectsBadSignature(t *testing.T) {
	d := Deps{WebhookSecret: "s", Orch: &fakeOrch{}}
	body := []byte(`{}`)
	r := httptest.NewRequest("POST", "/api/v1/github/webhook", bytes.NewReader(body))
	r.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	r.Header.Set("X-GitHub-Event", "issues")
	w := httptest.NewRecorder()
	d.GitHubWebhookHandler()(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("code = %d", w.Code)
	}
}
func TestWebhookAcceptsSignedIssuesEvent(t *testing.T) {
	fo := &fakeOrch{}
	d := Deps{WebhookSecret: "s", Orch: fo}
	body := []byte(`{"action":"labeled","repository":{"full_name":"o/r"},"issue":{"number":15,"state":"open","labels":[{"name":"agentmon:epic"}]}}`)
	r := httptest.NewRequest("POST", "/api/v1/github/webhook", bytes.NewReader(body))
	r.Header.Set("X-Hub-Signature-256", signBody("s", body))
	r.Header.Set("X-GitHub-Event", "issues")
	w := httptest.NewRecorder()
	d.GitHubWebhookHandler()(w, r)
	if w.Code != http.StatusAccepted {
		t.Fatalf("code = %d body=%s", w.Code, w.Body.String())
	}
	if len(fo.ingested) != 1 || fo.ingested[0].Issue.Number != 15 {
		t.Fatalf("ingested = %+v", fo.ingested)
	}
}
