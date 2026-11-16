package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/olegpustovit/moneybag/backend/internal/components/identity"
)

func TestIdentityCookieAndOriginBoundary(t *testing.T) {
	const origin = "https://moneybag.example.test"
	for _, mode := range []bool{false, true} {
		h := &Handler{cfg: Config{IdentityMode: mode, PublicURL: origin, TailnetURL: "https://tailnet.example.test"}}
		recorder := httptest.NewRecorder()
		h.writeSessionCookie(recorder, "fixture-token", 7*24*60*60)
		cookies := recorder.Result().Cookies()
		if len(cookies) != 1 || cookies[0].Name != h.sessionCookieName() || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].Path != "/" || cookies[0].Domain != "" || cookies[0].SameSite != http.SameSiteLaxMode {
			t.Fatal("browser cookie boundary changed")
		}
		if mode && cookies[0].Name != "__Host-moneybag_session" {
			t.Fatal("ID cookie lacks host prefix")
		}
		for _, requestOrigin := range []string{"", origin, h.cfg.TailnetURL, "https://wrong.example.test"} {
			request := httptest.NewRequest("POST", origin+"/logout", nil)
			request.Header.Set("Origin", requestOrigin)
			request.Header.Set("X-CSRF-Token", "fixture-csrf")
			want := requestOrigin == origin || !mode && (requestOrigin == "" || requestOrigin == h.cfg.TailnetURL)
			if h.csrf(request, identity.Session{CSRF: "fixture-csrf"}) != want {
				t.Fatal("browser origin policy changed")
			}
			request.Header.Set("X-CSRF-Token", "wrong")
			if h.csrf(request, identity.Session{CSRF: "fixture-csrf"}) {
				t.Fatal("wrong CSRF token accepted")
			}
		}
	}
	h := &Handler{cfg: Config{IdentityMode: true, PublicURL: "http://localhost:6459"}}
	recorder := httptest.NewRecorder()
	h.writeSessionCookie(recorder, "", -1)
	if cookie := recorder.Result().Cookies()[0]; !cookie.Secure || cookie.Name != "__Host-moneybag_session" || cookie.MaxAge != -1 {
		t.Fatal("ID loopback logout cookie was weakened")
	}
}
