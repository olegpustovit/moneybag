package handlers

import (
	"errors"
	"net"
	"net/url"
	"strings"
)

type IdentityConfig struct {
	Enabled                        bool
	AccessEnabled                  bool
	Issuer, ClientID, ClientSecret string
	CookieKey                      [32]byte
}

func (c IdentityConfig) Validate(publicURL, tailnetURL string) error {
	if c.AccessEnabled && (!c.Enabled || strings.TrimSpace(c.ClientSecret) == "") {
		return errors.New("central access enforcement requires enabled confidential Microapps ID sign-in")
	}
	if !c.Enabled {
		return nil
	}
	if strings.TrimSpace(c.ClientID) == "" || len(c.ClientID) > 255 || c.CookieKey == [32]byte{} {
		return errors.New("Microapps ID requires a client ID and an independent nonzero 32-byte cookie key")
	}
	if _, err := identityURL(c.Issuer, false); err != nil {
		return errors.New("Microapps ID issuer must use HTTPS or loopback HTTP without credentials, query or fragment")
	}
	hosts := map[string]string{}
	for _, origin := range []string{publicURL, tailnetURL} {
		if origin == "" {
			continue
		}
		u, err := identityURL(origin, true)
		if err != nil {
			return errors.New("Microapps ID callbacks require canonical HTTPS or loopback HTTP origins")
		}
		if previous, ok := hosts[u.Host]; ok && previous != origin {
			return errors.New("Microapps ID callback origins must use distinct hosts or ports")
		}
		hosts[u.Host] = origin
	}
	if publicURL == "" {
		return errors.New("Microapps ID requires a public origin")
	}
	return nil
}
func identityURL(raw string, origin bool) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
		return nil, errors.New("invalid Microapps ID URL")
	}
	ip := net.ParseIP(u.Hostname())
	loopback := u.Hostname() == "localhost" || ip != nil && ip.IsLoopback()
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return nil, errors.New("insecure Microapps ID URL")
	}
	if origin && (u.Path != "" || raw != u.Scheme+"://"+strings.ToLower(u.Host) || u.Scheme == "https" && u.Port() == "443" || u.Scheme == "http" && u.Port() == "80") {
		return nil, errors.New("invalid Microapps ID origin")
	}
	return u, nil
}
