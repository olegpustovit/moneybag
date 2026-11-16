package handlers

import (
	"errors"
	"net"
	"net/url"
	"strings"
)

type IdentityConfig struct {
	Enabled                        bool
	Issuer, ClientID, ClientSecret string
	CookieKey                      [32]byte
}

func (c IdentityConfig) Validate(publicURL, tailnetURL string) error {
	if !c.Enabled {
		return nil
	}
	if strings.TrimSpace(c.ClientID) == "" || len(c.ClientID) > 255 || c.CookieKey == [32]byte{} {
		return errors.New("Identity requires a client ID and an independent nonzero 32-byte cookie key")
	}
	if _, err := identityURL(c.Issuer, false); err != nil {
		return errors.New("Identity issuer must use HTTPS or loopback HTTP without credentials, query or fragment")
	}
	hosts := map[string]string{}
	for _, origin := range []string{publicURL, tailnetURL} {
		if origin == "" {
			continue
		}
		u, err := identityURL(origin, true)
		if err != nil {
			return errors.New("Identity callbacks require canonical HTTPS or loopback HTTP origins")
		}
		if previous, ok := hosts[u.Host]; ok && previous != origin {
			return errors.New("Identity callback origins must use distinct hosts or ports")
		}
		hosts[u.Host] = origin
	}
	if publicURL == "" {
		return errors.New("Identity requires a public origin")
	}
	return nil
}
func identityURL(raw string, origin bool) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
		return nil, errors.New("invalid Identity URL")
	}
	ip := net.ParseIP(u.Hostname())
	loopback := u.Hostname() == "localhost" || ip != nil && ip.IsLoopback()
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return nil, errors.New("insecure Identity URL")
	}
	if origin && (u.Path != "" || raw != u.Scheme+"://"+strings.ToLower(u.Host) || u.Scheme == "https" && u.Port() == "443" || u.Scheme == "http" && u.Port() == "80") {
		return nil, errors.New("invalid Identity origin")
	}
	return u, nil
}
