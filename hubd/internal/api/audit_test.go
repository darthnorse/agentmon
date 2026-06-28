package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"agentmon/hubd/internal/authz"
	"agentmon/hubd/internal/db"
)

type stubAudit struct{ rows []db.AuditEntry }

func (s stubAudit) Recent(_ context.Context, _ int) ([]db.AuditEntry, error) { return s.rows, nil }

func TestAuditHandlerReturnsRows(t *testing.T) {
	d := testDeps(nil)
	d.AuditRepo = stubAudit{rows: []db.AuditEntry{{ID: "a1", Action: "login.success", Result: "allow", Resource: "user:u1", PrincipalID: "u1"}}}
	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/audit", nil), authz.Principal{ID: "u1"})
	w := httptest.NewRecorder()
	d.AuditHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("code %d", w.Code)
	}
	var got []map[string]string
	json.NewDecoder(w.Body).Decode(&got)
	if len(got) != 1 || got[0]["action"] != "login.success" {
		t.Fatalf("got %+v", got)
	}
}

func TestAuditHandlerDeniesEmptyPrincipal(t *testing.T) {
	d := testDeps(nil)
	d.AuditRepo = stubAudit{}
	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/audit", nil), authz.Principal{})
	w := httptest.NewRecorder()
	d.AuditHandler()(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("code %d", w.Code)
	}
}
