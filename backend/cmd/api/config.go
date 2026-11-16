package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/olegpustovit/moneybag/backend/cmd/api/handlers"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type config struct {
	Identity                                                                    handlers.IdentityConfig
	Listen, PublicURL, TailnetURL, DatabaseURL, SetupToken, LogFormat, Chromium string
	Toolsets                                                                    map[string]bool
	SecretKey                                                                   []byte
}

func env(key, def string) string {
	if v, ok := os.LookupEnv("MONEYBAG_" + key); ok {
		return v
	}
	return def
}
func loadConfig() (config, error) {
	values, err := vaultValues()
	if err != nil {
		return config{}, err
	}
	env := func(key, def string) string {
		if v, ok := os.LookupEnv("MONEYBAG_" + key); ok {
			return v
		}
		if v, ok := values[key]; ok {
			return v
		}
		return def
	}
	c := config{DatabaseURL: env("DATABASE_URL", ""), Listen: env("LISTEN", "127.0.0.1:6459"), PublicURL: strings.TrimRight(env("PUBLIC_URL", "https://moneybag.microapps.space"), "/"), TailnetURL: strings.TrimRight(env("TAILNET_URL", ""), "/"), SetupToken: env("SETUP_TOKEN", ""), LogFormat: env("LOG_FORMAT", "text"), Chromium: env("CHROMIUM_BIN", "")}
	if _, _, e := net.SplitHostPort(c.Listen); e != nil {
		return c, fmt.Errorf("MONEYBAG_LISTEN must be host:port")
	}
	u, e := url.Parse(c.PublicURL)
	if e != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"))) {
		return c, fmt.Errorf("MONEYBAG_PUBLIC_URL must be an HTTPS origin or HTTP loopback origin")
	}
	if c.TailnetURL != "" {
		u, e := url.Parse(c.TailnetURL)
		if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
			return c, fmt.Errorf("MONEYBAG_TAILNET_URL must be an HTTPS origin")
		}
	}
	if c.DatabaseURL == "" {
		return c, fmt.Errorf("MONEYBAG_DATABASE_URL is required")
	}
	if _, err := pgxpool.ParseConfig(c.DatabaseURL); err != nil {
		return c, fmt.Errorf("MONEYBAG_DATABASE_URL must be a PostgreSQL connection string")
	}
	if c.LogFormat != "text" && c.LogFormat != "json" {
		return c, fmt.Errorf("MONEYBAG_LOG_FORMAT must be text or json")
	}
	if key := env("SECRET_KEY", ""); key != "" {
		c.SecretKey, e = base64.StdEncoding.DecodeString(key)
		if e != nil || len(c.SecretKey) != 32 {
			return c, fmt.Errorf("MONEYBAG_SECRET_KEY must be base64 encoding of 32 bytes")
		}
	}

	enabled, e := strconv.ParseBool(env("ID_ENABLED", "false"))
	if e != nil {
		return c, fmt.Errorf("MONEYBAG_ID_ENABLED must be true or false")
	}
	c.Identity = handlers.IdentityConfig{Enabled: enabled, Issuer: env("ID_ISSUER", "https://id.microapps.space"), ClientID: env("ID_CLIENT_ID", ""), ClientSecret: env("ID_CLIENT_SECRET", "")}
	if enabled {
		key, err := base64.StdEncoding.DecodeString(env("ID_COOKIE_KEY", ""))
		if err != nil || len(key) != 32 {
			return c, fmt.Errorf("MONEYBAG_ID_COOKIE_KEY must be base64 encoding of an independent 32-byte key")
		}
		copy(c.Identity.CookieKey[:], key)
		if bytes.Equal(key, c.SecretKey) {
			return c, fmt.Errorf("MONEYBAG_ID_COOKIE_KEY must be independent of MONEYBAG_SECRET_KEY")
		}
		if err := c.Identity.Validate(c.PublicURL, c.TailnetURL); err != nil {
			return c, err
		}
	}
	allowed := map[string]bool{"account": true, "ledger": true, "rates": true, "budget": true, "tables": true, "model": true, "dashboard": true, "audit": true, "classify": true, "import": true, "reconcile": true, "admin": true}
	c.Toolsets = map[string]bool{}
	for _, set := range strings.FieldsFunc(env("TOOLSETS", ""), func(r rune) bool { return r == ',' || r == ' ' }) {
		if !allowed[set] {
			return c, fmt.Errorf("unknown MONEYBAG_TOOLSETS entry %q", set)
		}
		c.Toolsets[set] = true
	}
	return c, nil
}
