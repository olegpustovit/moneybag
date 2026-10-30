// Package preview renders dashboards through the real SPA in headless Chromium.
package preview

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

const (
	tokenTTL      = 60 * time.Second
	maxTokens     = 16
	maxReportRows = 500
	firstHeight   = 1200
	maxHeight     = 6000
	DefaultWidth  = 1440
)

// ErrNoReport means the page loaded or timed out without ever posting its report.
var ErrNoReport = errors.New("the page never reported within the render budget, so the image would be blank or partial; check that the view endpoint and SPA load")

// Target is the only thing a render token can read.
type Target struct {
	Dashboard string
	Version   string
	Params    map[string]any
	Width     int
	Theme     string
	// Route, when set, makes this a route token: the real app page at that path, read-only, instead of a dashboard.
	Route string
}

var blockedRoutes = []string{"/login", "/logout", "/setup", "/register", "/oauth", "/confirm", "/secure", "/api", "/render"}

// ValidRoute accepts only a same-origin app page path.
func ValidRoute(route string) (string, error) {
	bad := fmt.Errorf("route must be an app page path such as /budget, not an external URL, an auth page or /api")
	if !strings.HasPrefix(route, "/") || strings.Contains(route, "//") || strings.ContainsAny(route, "\\\x00\r\n\t ") {
		return "", bad
	}
	u, err := url.Parse(route)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return "", bad
	}
	clean := strings.ToLower(path.Clean(u.Path))
	for _, blocked := range blockedRoutes {
		if clean == blocked || strings.HasPrefix(clean, blocked+"/") {
			return "", bad
		}
	}
	return route, nil
}

// Redirect is where a route token's capture lands, carrying the theme for the page to apply.
func (t Target) Redirect() string {
	u, _ := url.Parse(t.Route)
	if t.Theme == "dark" {
		q := u.Query()
		q.Set("theme", "dark")
		u.RawQuery = q.Encode()
	}
	return u.String()
}

type Box struct {
	Component string  `json:"component"`
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
	W         float64 `json:"w"`
	H         float64 `json:"h"`
}

// Checks lists offending component ids; TimeScopeApplies is null when the dashboard has no page clock.
type Checks struct {
	Overflow         []string `json:"overflow"`
	LabelCollisions  []string `json:"label_collisions"`
	EmptyPanels      []string `json:"empty_panels"`
	TimeScopeApplies *bool    `json:"time_scope_applies"`
}

// Report is what the page posts to /api/render/{token}/report.
type Report struct {
	Layout   []Box   `json:"layout"`
	Checks   Checks  `json:"checks"`
	TimingMS float64 `json:"timing_ms"`
}

// Failures returns one readable line per failed check; empty means the render gate passes.
func (r Report) Failures() []string {
	out := []string{}
	for _, check := range []struct {
		name string
		ids  []string
	}{{"content overflows", r.Checks.Overflow}, {"labels collide", r.Checks.LabelCollisions}, {"panel is empty", r.Checks.EmptyPanels}} {
		if len(check.ids) > 0 {
			out = append(out, check.name+": "+strings.Join(check.ids, ", "))
		}
	}
	if r.Checks.TimeScopeApplies != nil && !*r.Checks.TimeScopeApplies {
		out = append(out, "the page time range does not apply to its panels")
	}
	return out
}

// Session is a token's binding: one principal, any grant, one dashboard, version and params.
type Session struct {
	Principal principal.Principal
	GrantHash string
	Target    Target
	Expires   time.Time
	report    *Report
}

// Capture screenshots url; screenshot.Renderer provides it, tests stub it.
type Capture func(ctx context.Context, url string, width, height int) ([]byte, error)

type Service struct {
	mu       sync.Mutex
	sessions map[string]*Session
	base     string
	capture  Capture
}

// New takes the app's own loopback base URL; a nil capture means no renderer is configured.
func New(base string, capture Capture) *Service {
	return &Service{sessions: map[string]*Session{}, base: base, capture: capture}
}

func (s *Service) Available() bool { return s != nil && s.capture != nil }

// Issue creates a 60 second read-only token.
func (s *Service) Issue(p principal.Principal, grantHash string, t Target) (string, error) {
	if err := p.Require("ledger:read"); err != nil {
		return "", err
	}
	if t.Route != "" {
		if _, err := ValidRoute(t.Route); err != nil {
			return "", err
		}
		if grantHash != "" {
			return "", fmt.Errorf("routes are only for the owner")
		}
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for token, session := range s.sessions {
		if !now.Before(session.Expires) {
			delete(s.sessions, token)
		}
	}
	if len(s.sessions) >= maxTokens {
		return "", fmt.Errorf("too many renders in progress; retry in a minute")
	}
	token := rand.Text()
	s.sessions[token] = &Session{Principal: p, GrantHash: grantHash, Target: t, Expires: now.Add(tokenTTL)}
	return token, nil
}

// Resolve returns a live token's binding, or not found for an unknown or expired token.
func (s *Service) Resolve(token string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.sessions[token]
	if session == nil || !time.Now().Before(session.Expires) {
		return Session{}, principal.ErrNotFound
	}
	return *session, nil
}

// Report stores the page's latest report; a second pass at the full height replaces the first.
func (s *Service) Report(token string, report Report) error {
	if len(report.Layout) > maxReportRows || len(report.Checks.Overflow) > maxReportRows || len(report.Checks.LabelCollisions) > maxReportRows || len(report.Checks.EmptyPanels) > maxReportRows {
		return fmt.Errorf("report exceeds %d rows", maxReportRows)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.sessions[token]
	if session == nil || !time.Now().Before(session.Expires) {
		return principal.ErrNotFound
	}
	session.report = &report
	return nil
}

func (s *Service) take(token string) (Report, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.sessions[token]
	if session == nil || session.report == nil {
		return Report{}, false
	}
	return *session.report, true
}

func (s *Service) Revoke(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, token)
}

// Result is one finished render.
type Result struct {
	PNG      []byte
	Report   Report
	RenderMS int64
}

// Render opens the SPA at /render/{token} and fails unless the page reported.
func (s *Service) Render(ctx context.Context, p principal.Principal, grantHash string, t Target) (Result, error) {
	if !s.Available() {
		return Result{}, fmt.Errorf("renderer not configured")
	}
	if t.Width == 0 {
		t.Width = DefaultWidth
	}
	started := time.Now()
	token, err := s.Issue(p, grantHash, t)
	if err != nil {
		return Result{}, err
	}
	defer s.Revoke(token)
	url := s.base + "/render/" + token
	if t.Route != "" {
		url += "/page"
	} else if t.Theme == "dark" {
		url += "?theme=dark"
	}
	png, err := s.capture(ctx, url, t.Width, firstHeight)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return Result{}, err
		}
		return Result{}, fmt.Errorf("%w (%v)", ErrNoReport, err)
	}
	report, ok := s.take(token)
	if !ok {
		return Result{}, ErrNoReport
	}
	if height := report.height(); height > firstHeight {
		// The first pass measured the page; a second one shows all of it. A failed retake keeps the top of the page.
		if taller, err := s.capture(ctx, url, t.Width, min(height, maxHeight)); err == nil {
			png = taller
			if again, ok := s.take(token); ok {
				report = again
			}
		}
	}
	return Result{PNG: png, Report: report, RenderMS: time.Since(started).Milliseconds()}, nil
}

func (r Report) height() int {
	bottom := 0.0
	for _, box := range r.Layout {
		bottom = max(bottom, box.Y+box.H)
	}
	return int(bottom)
}

// Gate is the publish render gate. Without a renderer it passes, as the contract allows.
// Call it before entering a workflow: Chromium takes seconds and workflows serialize every writer.
func (s *Service) Gate(ctx context.Context, p principal.Principal, grantHash string, t Target) ([]string, error) {
	if !s.Available() {
		return nil, nil
	}
	result, err := s.Render(ctx, p, grantHash, t)
	if err != nil {
		return nil, err
	}
	return result.Report.Failures(), nil
}
