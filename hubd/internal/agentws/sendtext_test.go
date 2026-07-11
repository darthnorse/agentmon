package agentws

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/directive"
	"agentmon/shared"
)

func TestFirstPaneID(t *testing.T) {
	ss := []shared.Session{{Name: "x"}, {Name: "epic", Windows: []shared.Window{{}, {Panes: []shared.Pane{{ID: "%1"}}}}}}
	if got, ok := FirstPaneID(ss, "epic"); !ok || got != "%1" {
		t.Fatalf("%q %v", got, ok)
	}
}
func TestSendText(t *testing.T) {
	got := make(chan string, 1)
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/panes/%1/io") || r.URL.Query().Get("mode") != "rw" || r.Header.Get("Authorization") != "Bearer b" || r.Header.Get("X-AgentMon-Directive") == "" || r.Header.Get("X-AgentMon-Request-Id") == "" {
			http.Error(w, "bad", 400)
			return
		}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		typ, b, err := c.ReadMessage()
		if err == nil && typ == websocket.BinaryMessage {
			got <- string(b)
		}
	}))
	defer srv.Close()
	m := &directive.Minter{Now: func() time.Time { return time.Unix(0, 0) }, NewNonce: func() string { return "n" }, NewRequestID: func() string { return "r" }}
	err := SendText(context.Background(), db.Server{ID: "h", URL: srv.URL, Bearer: "b", SigningKey: "key"}, m, "u", "", "epic", "approved: option A\n", []shared.Session{{Name: "epic", Windows: []shared.Window{{Panes: []shared.Pane{{ID: "%1"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case s := <-got:
		if s != "approved: option A\n" {
			t.Fatalf("%q", s)
		}
	case <-time.After(time.Second):
		t.Fatal("no frame")
	}
}
