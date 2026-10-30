package handlers

import (
	"strings"
	"testing"
)

func TestAuthTemplates(t *testing.T) {
	templates, err := parseTemplates()
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range []authPage{{}, {Setup: true}, {Register: true}, {Consent: true, Scopes: []string{"ledger:read"}}} {
		var rendered strings.Builder
		if err := templates.ExecuteTemplate(&rendered, "auth.html", data); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(rendered.String(), assetPaths["auth.css"]) || !strings.Contains(rendered.String(), `src="/theme.js"`) {
			t.Fatal("auth page missing stylesheet")
		}
	}
	var invite strings.Builder
	if err := templates.ExecuteTemplate(&invite, "invitation.html", "https://example.test/register"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(invite.String(), assetPaths["auth.css"]) || !strings.Contains(invite.String(), `src="/theme.js"`) {
		t.Fatal("invitation page missing stylesheet")
	}
}
