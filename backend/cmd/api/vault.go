package main

import (
	"context"
	"fmt"
	vault "github.com/hashicorp/vault/api"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

func vaultValues() (map[string]string, error) {
	enabled, e := strconv.ParseBool(env("VAULT_ENABLED", "false"))
	if e != nil {
		return nil, fmt.Errorf("MONEYBAG_VAULT_ENABLED must be boolean")
	}
	if !enabled {
		return nil, nil
	}
	addr, path := env("VAULT_ADDR", ""), env("VAULT_PATH", "")
	u, e := url.Parse(addr)
	if e != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost"))) || path == "" {
		return nil, fmt.Errorf("Vault requires MONEYBAG_VAULT_ADDR (HTTPS or loopback) and MONEYBAG_VAULT_PATH")
	}
	client, e := vault.NewClient(&vault.Config{Address: addr, HttpClient: &http.Client{Timeout: 10 * time.Second, Transport: http.DefaultTransport.(*http.Transport).Clone()}, Timeout: 10 * time.Second, MaxRetries: 1})
	if e != nil {
		return nil, fmt.Errorf("initialize Vault client")
	}
	if ns := env("VAULT_NAMESPACE", ""); ns != "" {
		client.SetNamespace(ns)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	switch env("VAULT_AUTH_METHOD", "approle") {
	case "approle":
		role, secret := env("VAULT_ROLE_ID", ""), env("VAULT_SECRET_ID", "")
		if role == "" || secret == "" {
			return nil, fmt.Errorf("Vault AppRole requires MONEYBAG_VAULT_ROLE_ID and MONEYBAG_VAULT_SECRET_ID")
		}
		auth, e := client.Logical().WriteWithContext(ctx, "auth/approle/login", map[string]any{"role_id": role, "secret_id": secret})
		if e != nil || auth == nil || auth.Auth == nil || auth.Auth.ClientToken == "" {
			return nil, fmt.Errorf("Vault AppRole authentication failed")
		}
		client.SetToken(auth.Auth.ClientToken)
		defer func() {
			revoke, stop := context.WithTimeout(context.Background(), 3*time.Second)
			defer stop()
			_ = client.Auth().Token().RevokeSelfWithContext(revoke, "")
		}()
	case "token":
		if token := env("VAULT_TOKEN", ""); token != "" {
			client.SetToken(token)
		} else {
			return nil, fmt.Errorf("MONEYBAG_VAULT_TOKEN is required for token auth")
		}
	default:
		return nil, fmt.Errorf("MONEYBAG_VAULT_AUTH_METHOD must be approle or token")
	}
	secret, e := client.KVv2(env("VAULT_MOUNT", "kv")).Get(ctx, path)
	if e != nil || secret == nil {
		return nil, fmt.Errorf("Vault configuration read failed")
	}
	allowed := map[string]string{"listen": "LISTEN", "public_url": "PUBLIC_URL", "mongo_uri": "MONGO_URI", "mongo_db": "MONGO_DB", "setup_token": "SETUP_TOKEN", "log_format": "LOG_FORMAT", "toolsets": "TOOLSETS", "secret_key": "SECRET_KEY", "chromium_bin": "CHROMIUM_BIN"}
	out := map[string]string{}
	for key, value := range secret.Data {
		name, ok := allowed[key]
		if !ok {
			return nil, fmt.Errorf("unknown Vault configuration key %q", key)
		}
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("Vault configuration key %q must be a string", key)
		}
		out[name] = text
	}
	return out, nil
}
