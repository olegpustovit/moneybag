package main

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConfigVaultPrecedenceAndSecretValidation(t *testing.T) {
	for _, key := range []string{"LISTEN", "PUBLIC_URL", "MONGO_URI", "MONGO_DB", "SETUP_TOKEN", "LOG_FORMAT", "TOOLSETS", "SECRET_KEY"} {
		t.Setenv("MONEYBAG_"+key, "")
	}
	t.Setenv("MONEYBAG_PUBLIC_URL", "http://127.0.0.1:6459")
	t.Setenv("MONEYBAG_MONGO_DB", "test")
	t.Setenv("MONEYBAG_LOG_FORMAT", "text")
	t.Setenv("MONEYBAG_LISTEN", "127.0.0.1:6459")
	t.Setenv("MONEYBAG_VAULT_ENABLED", "false")
	t.Setenv("MONEYBAG_UI", "legacy")
	c, e := loadConfig()
	if e != nil || c.MongoDB != "test" {
		t.Fatal(c.MongoDB, e)
	}
	t.Setenv("MONEYBAG_UI", "invalid")
	if _, e := loadConfig(); e == nil {
		t.Fatal("invalid UI mode accepted")
	}
	t.Setenv("MONEYBAG_UI", "spa")
	if cfg, e := loadConfig(); e != nil || cfg.UI != "spa" {
		t.Fatalf("SPA mode: %v", e)
	}
	t.Setenv("MONEYBAG_UI", "legacy")
	t.Setenv("MONEYBAG_SECRET_KEY", "secret-must-not-appear")
	_, e = loadConfig()
	if e == nil || strings.Contains(e.Error(), "secret-must-not-appear") {
		t.Fatal("secret validation/redaction", e)
	}
	t.Setenv("MONEYBAG_SECRET_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("MONEYBAG_TOOLSETS", "ledger,dashboard")
	c, e = loadConfig()
	if e != nil || len(c.SecretKey) != 32 || len(c.Toolsets) != 2 {
		t.Fatal("typed config", e)
	}
	t.Setenv("MONEYBAG_TOOLSETS", "typo")
	if _, e = loadConfig(); e == nil {
		t.Fatal("unknown toolset accepted")
	}
	t.Setenv("MONEYBAG_TOOLSETS", "")
	revoked := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/auth/approle/login":
			io.WriteString(w, `{"auth":{"client_token":"synthetic-token"}}`)
		case "/v1/kv/data/moneybag/test":
			if r.Header.Get("X-Vault-Token") != "synthetic-token" {
				t.Error("missing Vault token")
			}
			io.WriteString(w, `{"data":{"data":{"listen":"127.0.0.1:1234","mongo_db":"vault-db"},"metadata":{}}}`)
		case "/v1/auth/token/revoke-self":
			revoked = true
			w.WriteHeader(204)
		default:
			t.Error(r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	t.Setenv("MONEYBAG_VAULT_ENABLED", "true")
	t.Setenv("MONEYBAG_VAULT_ADDR", server.URL)
	t.Setenv("MONEYBAG_VAULT_PATH", "moneybag/test")
	t.Setenv("MONEYBAG_VAULT_ROLE_ID", "synthetic-role")
	t.Setenv("MONEYBAG_VAULT_SECRET_ID", "synthetic-secret")
	t.Setenv("MONEYBAG_VAULT_AUTH_METHOD", "approle")
	values, e := vaultValues()
	if e != nil || values["MONGO_DB"] != "vault-db" || !revoked {
		t.Fatal("Vault startup read/revoke", e)
	}
	c, e = loadConfig()
	if e != nil || c.Listen != "127.0.0.1:6459" || c.MongoDB != "test" {
		t.Fatal("environment must override Vault", e)
	}
	server.Close()
	if _, e = loadConfig(); e == nil {
		t.Fatal("configured Vault silently fell back")
	}
}
