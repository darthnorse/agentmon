package authn

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentmon/hubd/internal/authz"
	"agentmon/hubd/internal/db"
)

type stubPwStore struct {
	u         db.User
	getErr    error
	setCalled bool
	setHash   string
}

func (s *stubPwStore) GetUserByUsername(_ context.Context, _ string) (db.User, error) {
	return s.u, s.getErr
}
func (s *stubPwStore) SetPassword(_ context.Context, _, _, _, hash string) error {
	s.setCalled = true
	s.setHash = hash
	return nil
}

type stubPwAudit struct{ n int }

func (s *stubPwAudit) PasswordChange(_ context.Context, _, _, _ string) { s.n++ }

func pwReq(body string, p authz.Principal) (*http.Request, *httptest.ResponseRecorder) {
	r := httptest.NewRequest("POST", "/api/v1/auth/password", strings.NewReader(body))
	if p.ID != "" {
		r = r.WithContext(ContextWithPrincipal(r.Context(), p))
	}
	return r, httptest.NewRecorder()
}

func TestChangePasswordHappyPath(t *testing.T) {
	hash, _ := HashPassword("oldpw")
	store := &stubPwStore{u: db.User{ID: "u1", Username: "patrik", DisplayName: "P", PasswordHash: hash, Status: "active"}}
	aud := &stubPwAudit{}
	d := PasswordDeps{Users: store, Audit: aud}
	r, w := pwReq(`{"currentPassword":"oldpw","newPassword":"newpassword1"}`, authz.Principal{ID: "u1", Username: "patrik"})
	d.ChangeHandler()(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("code %d: %s", w.Code, w.Body)
	}
	if !store.setCalled {
		t.Fatal("SetPassword was not called")
	}
	if ok, _ := VerifyPassword(store.setHash, "newpassword1"); !ok {
		t.Fatal("stored hash does not verify the new password")
	}
	if aud.n != 1 {
		t.Fatalf("password change must be audited once, got %d", aud.n)
	}
}

func TestChangePasswordRejects(t *testing.T) {
	hash, _ := HashPassword("oldpw")
	cases := []struct {
		name string
		body string
		p    authz.Principal
		want int
	}{
		{"wrong current", `{"currentPassword":"WRONG","newPassword":"newpassword1"}`, authz.Principal{ID: "u1", Username: "patrik"}, http.StatusUnauthorized},
		{"short new", `{"currentPassword":"oldpw","newPassword":"short"}`, authz.Principal{ID: "u1", Username: "patrik"}, http.StatusBadRequest},
		{"no principal", `{"currentPassword":"oldpw","newPassword":"newpassword1"}`, authz.Principal{}, http.StatusUnauthorized},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := &stubPwStore{u: db.User{ID: "u1", Username: "patrik", PasswordHash: hash, Status: "active"}}
			d := PasswordDeps{Users: store, Audit: &stubPwAudit{}}
			r, w := pwReq(c.body, c.p)
			d.ChangeHandler()(w, r)
			if w.Code != c.want {
				t.Fatalf("code %d, want %d", w.Code, c.want)
			}
			if store.setCalled {
				t.Fatal("a rejected change must NOT set the password")
			}
		})
	}
}

func TestLoginFlagsDefaultPasswordForChange(t *testing.T) {
	hash, _ := HashPassword(DefaultPassword)
	d := deps(t, db.User{ID: "u1", Username: DefaultUsername, DisplayName: "admin", PasswordHash: hash, Status: "active"}, nil)
	r := httptest.NewRequest("POST", "/api/v1/auth/login",
		strings.NewReader(fmt.Sprintf(`{"username":%q,"password":%q}`, DefaultUsername, DefaultPassword)))
	w := httptest.NewRecorder()
	d.LoginHandler()(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("code %d: %s", w.Code, w.Body)
	}
	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["mustChangePassword"] != true {
		t.Fatalf("default creds must flag mustChangePassword: %+v", resp)
	}
}

func TestLoginNonDefaultDoesNotFlagChange(t *testing.T) {
	hash, _ := HashPassword("realpassword")
	d := deps(t, db.User{ID: "u1", Username: "patrik", DisplayName: "P", PasswordHash: hash, Status: "active"}, nil)
	r := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"patrik","password":"realpassword"}`))
	w := httptest.NewRecorder()
	d.LoginHandler()(w, r)
	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["mustChangePassword"] == true {
		t.Fatalf("a non-default login must NOT flag mustChangePassword: %+v", resp)
	}
}
