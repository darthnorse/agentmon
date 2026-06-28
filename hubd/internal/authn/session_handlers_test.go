package authn

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLogoutDeletesSessionAndClearsCookie(t *testing.T) {
	st := NewStore(time.Hour)
	a := &Authenticator{Store: st, CookieName: "agentmon_session"}
	sess, _ := st.New("u1", "patrik", "Patrik")
	r := httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
	r.AddCookie(&http.Cookie{Name: "agentmon_session", Value: sess.Token})
	w := httptest.NewRecorder()
	a.LogoutHandler(false)(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("code %d", w.Code)
	}
	if _, ok := st.Get(sess.Token); ok {
		t.Fatal("session not deleted")
	}
	c := w.Result().Cookies()[0]
	if c.MaxAge >= 0 {
		t.Fatalf("cookie not cleared: %+v", c)
	}
}

func TestMeReturnsPrincipalAndCSRF(t *testing.T) {
	st := NewStore(time.Hour)
	a := &Authenticator{Store: st, CookieName: "agentmon_session"}
	sess, _ := st.New("u1", "patrik", "Patrik")
	r := httptest.NewRequest("GET", "/api/v1/me", nil)
	r.AddCookie(&http.Cookie{Name: "agentmon_session", Value: sess.Token})
	w := httptest.NewRecorder()
	a.MeHandler()(w, r)
	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["principalId"] != "u1" || resp["username"] != "patrik" || resp["csrfToken"] != sess.CSRFToken {
		t.Fatalf("resp %+v", resp)
	}
}
