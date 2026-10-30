package main

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

type config struct {
	Listen, PublicURL, TailnetURL, MongoURI, MongoDB, SetupToken, LogFormat, Chromium string
	Toolsets                                                                          map[string]bool
	SecretKey                                                                         []byte
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
	c := config{Listen: env("LISTEN", "127.0.0.1:6459"), PublicURL: strings.TrimRight(env("PUBLIC_URL", "https://moneybag.microapps.space"), "/"), TailnetURL: strings.TrimRight(env("TAILNET_URL", ""), "/"), MongoURI: env("MONGO_URI", "mongodb://127.0.0.1:27017/moneybag"), MongoDB: env("MONGO_DB", "moneybag"), SetupToken: env("SETUP_TOKEN", ""), LogFormat: env("LOG_FORMAT", "text"), Chromium: env("CHROMIUM_BIN", "")}
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
	if c.MongoDB == "" || strings.ContainsAny(c.MongoDB, "/\\. \"$\x00") {
		return c, fmt.Errorf("invalid MONEYBAG_MONGO_DB")
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
	allowed := map[string]bool{"account": true, "ledger": true, "rates": true, "budget": true, "tables": true, "model": true, "dashboard": true, "audit": true, "classify": true, "import": true, "reconcile": true}
	c.Toolsets = map[string]bool{}
	for _, set := range strings.FieldsFunc(env("TOOLSETS", ""), func(r rune) bool { return r == ',' || r == ' ' }) {
		if !allowed[set] {
			return c, fmt.Errorf("unknown MONEYBAG_TOOLSETS entry %q", set)
		}
		c.Toolsets[set] = true
	}
	return c, nil
}
