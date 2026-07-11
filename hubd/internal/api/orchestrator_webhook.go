package api

import (
	"context"
	"io"
	"log"
	"net/http"

	"agentmon/hubd/internal/github"
)

type OrchestratorAPI interface {
	IngestWebhook(ctx context.Context, ev github.Event) error
	Wake()
	Approve(ctx context.Context, epicID, source string) error
	Retry(ctx context.Context, epicID, source string) error
	Cancel(ctx context.Context, epicID, source string) error
	RunIssue(ctx context.Context, projectID string, issue int) error
}

const maxWebhookBody = 1 << 20

func (d Deps) GitHubWebhookHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Orch == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "orchestrator disabled")
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "bad request")
			return
		}
		if !github.VerifySignature(d.WebhookSecret, body, r.Header.Get("X-Hub-Signature-256")) {
			writeJSONError(w, http.StatusForbidden, "bad signature")
			return
		}
		kind := r.Header.Get("X-GitHub-Event")
		if kind == "ping" {
			writeJSON(w, http.StatusOK, map[string]string{"ok": "pong"})
			return
		}
		ev, err := github.ParseEvent(kind, body)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "unparseable event")
			return
		}
		if err := d.Orch.IngestWebhook(r.Context(), ev); err != nil {
			log.Printf("webhook ingest: %v", err)
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"ok": "accepted"})
	}
}
