package oauth

import (
	"testing"
)

func TestRedirectAllowlistAndPKCE(t *testing.T) {
	for _, s := range []string{"https://chatgpt.com@evil.com/cb", "https://chatgpt.com/cb#fragment", "http://chatgpt.com/cb", "//chatgpt.com/cb", "javascript:alert(1)", "data:text/html,x", "file:///etc/passwd", "https:///nohost", "ws://localhost/cb"} {
		if AllowedRedirect(s) {
			t.Errorf("accepted %s", s)
		}
	}
	for _, s := range []string{"https://chatgpt.com/connector_platform_oauth_redirect", "https://claude.ai/api/mcp/auth_callback", "https://grok.com/cb", "https://agents.example.com:8443/oauth/callback", "http://127.0.0.1:8484/cb", "http://[::1]:9/cb", "cursor://anysphere.cursor-mcp/oauth/callback", "com.example.app:/oauth2redirect"} {
		if !AllowedRedirect(s) {
			t.Errorf("rejected %s", s)
		}
	}
	if Challenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk") != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Fatal("RFC7636 vector failed")
	}
}

func TestRedirectMatchesLoopbackAnyPort(t *testing.T) {
	reg := []string{"http://localhost/callback", "http://127.0.0.1/callback"}
	for raw, want := range map[string]bool{
		"http://localhost:60873/callback": true,
		"http://127.0.0.1:5000/callback":  true,
		"http://localhost/callback":       true,
		"http://localhost:1/other":        false,
		"https://localhost:1/callback":    false,
		"http://evil.com:1/callback":      false,
	} {
		if RedirectMatches(reg, raw) != want {
			t.Errorf("%s: want %v", raw, want)
		}
	}
}
