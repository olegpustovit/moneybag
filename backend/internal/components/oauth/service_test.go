package oauth

import (
	"testing"
)

func TestRedirectAllowlistAndPKCE(t *testing.T) {
	for _, s := range []string{"https://evil.com/cb", "https://chatgpt.com.evil.com/cb", "https://chatgpt.com@evil.com/cb", "https://chatgpt.com/cb#fragment", "http://chatgpt.com/cb", "https://chatgpt.com:8443/cb", "//chatgpt.com/cb"} {
		if AllowedRedirect(s) {
			t.Errorf("accepted %s", s)
		}
	}
	for _, s := range []string{"https://chatgpt.com/oauth/callback", "https://grok.com/cb", "http://127.0.0.1:8484/cb"} {
		if !AllowedRedirect(s) {
			t.Errorf("rejected %s", s)
		}
	}
	if Challenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk") != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Fatal("RFC7636 vector failed")
	}
}
