package main

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestIdentityConfigurationIsDefaultOffAndValidatedBeforeNetwork(t *testing.T) {
	for _, key := range []string{"ID_ENABLED", "ID_ISSUER", "ID_CLIENT_ID", "ID_CLIENT_SECRET", "ID_COOKIE_KEY", "SECRET_KEY", "TAILNET_URL"} {
		t.Setenv("MONEYBAG_"+key, "")
	}
	t.Setenv("MONEYBAG_VAULT_ENABLED", "false")
	t.Setenv("MONEYBAG_DATABASE_URL", "postgres://localhost/test")
	t.Setenv("MONEYBAG_PUBLIC_URL", "https://moneybag.example.test")
	t.Setenv("MONEYBAG_ID_ENABLED", "false")
	t.Setenv("MONEYBAG_ID_COOKIE_KEY", "unused-invalid-value")
	cfg, err := loadConfig()
	if err != nil || cfg.Identity.Enabled {
		t.Fatal("disabled configuration changed legacy startup", err)
	}
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	valid := func() {
		t.Setenv("MONEYBAG_ID_ENABLED", "true")
		t.Setenv("MONEYBAG_ID_ISSUER", "https://id.example.test")
		t.Setenv("MONEYBAG_ID_CLIENT_ID", "fixture-client")
		t.Setenv("MONEYBAG_ID_COOKIE_KEY", key)
		t.Setenv("MONEYBAG_SECRET_KEY", "")
		t.Setenv("MONEYBAG_PUBLIC_URL", "https://moneybag.example.test")
		t.Setenv("MONEYBAG_TAILNET_URL", "")
	}
	valid()
	cfg, err = loadConfig()
	if err != nil || !cfg.Identity.Enabled {
		t.Fatal("valid Identity configuration rejected", err)
	}
	for _, patch := range []struct{ key, value string }{{"ID_ENABLED", "invalid"}, {"ID_CLIENT_ID", ""}, {"ID_COOKIE_KEY", "secret-must-not-appear"}, {"ID_COOKIE_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32))}, {"ID_ISSUER", "http://remote.example.test"}, {"ID_ISSUER", "https://user:secret-must-not-appear@example.test"}, {"ID_ISSUER", "https://id.example.test/?"}, {"ID_ISSUER", "https://id.example.test/#"}, {"SECRET_KEY", key}, {"PUBLIC_URL", "https://moneybag.example.test/path"}, {"TAILNET_URL", "https://tailnet.example.test/#"}} {
		valid()
		t.Setenv("MONEYBAG_"+patch.key, patch.value)
		if _, err := loadConfig(); err == nil || strings.Contains(err.Error(), "secret-must-not-appear") {
			t.Fatal("invalid Identity configuration accepted or secret exposed", patch.key)
		}
	}
	valid()
	t.Setenv("MONEYBAG_TAILNET_URL", "https://moneybag.example.ts.net:8443")
	if _, err := loadConfig(); err != nil {
		t.Fatal("configured Tailnet origin rejected", err)
	}
}
