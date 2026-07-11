package api

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentmon/hubd/internal/authz"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/orchestrator"
	"agentmon/shared"
)

func TestBoardEvents(t *testing.T) {
	database := orchDB(t)
	database.CreateProject(context.Background(), db.Project{ID: "p1", Name: "p", Repo: "o/r", ServerID: "h1", Workdir: "/w", BaseBranch: "main", Provider: "claude", MaxParallel: 1})
	b := orchestrator.NewBoardBroadcaster()
	d := Deps{DB: database, BoardBcast: b, SSEHeartbeat: time.Hour}
	pr, pw := io.Pipe()
	rw := &pipeResponseWriter{pw: pw, header: make(http.Header)}
	ctx, cancel := context.WithCancel(context.Background())
	req := withPrincipal(httptest.NewRequest("GET", "/api/v1/orchestrator/events", nil).WithContext(ctx), authz.Principal{ID: "u1"})
	done := make(chan struct{})
	go func() { defer close(done); defer pw.Close(); d.OrchestratorEventsHandler()(rw, req) }()
	sc := bufio.NewScanner(pr)
	var all strings.Builder
	for sc.Scan() {
		all.WriteString(sc.Text())
		all.WriteByte('\n')
		if strings.Contains(all.String(), "event: board-snapshot") {
			break
		}
	}
	b.Publish(orchestrator.BoardChange{ProjectID: "p1", EpicID: "e1", Issue: 1, Stage: shared.EpicEscalated})
	for sc.Scan() {
		all.WriteString(sc.Text())
		all.WriteByte('\n')
		if strings.Contains(all.String(), `"stage":"escalated"`) {
			break
		}
	}
	cancel()
	<-done
	if !strings.Contains(all.String(), "event: board-snapshot") || !strings.Contains(all.String(), "event: board") {
		t.Fatalf("body=%s", all.String())
	}
}
