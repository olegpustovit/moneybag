package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/olegpustovit/moneybag/backend/internal/components/oauth"
	"github.com/olegpustovit/moneybag/backend/internal/components/preview"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

// Actual Fiber/compression/MCP, real PostgreSQL and the published SDK. The
// policy endpoint and headless capture are owned fakes; no finance/provider data.
func TestAccessActiveWorkRehearsal(t *testing.T) {
	if os.Getenv("MONEYBAG_ACCESS_REHEARSAL") != "1" {
		t.Skip("set MONEYBAG_ACCESS_REHEARSAL=1 for production-interval cancellation")
	}
	h, mode := newAccessHarness(t)
	// Isolate the HTTP/MCP propagation for the agent call: it has no redundant
	// capture timer to hide a missing request-cancellation link. The browser
	// capture retains the production guard; preview's own guard has unit tests.
	h.services.Previews.WithAccessGuard(func(ctx context.Context, userID string) (context.Context, context.CancelFunc, error) {
		p, _ := principal.From(ctx)
		if p.Actor.Kind == "agent" {
			work, release := context.WithCancel(ctx)
			return work, release, nil
		}
		return h.services.Identity.WatchAccess(ctx, userID)
	})
	const origin = "https://public.example.test"
	oa := oauth.NewService(oauth.NewPostgresRepository(h.db), h.services.Identity, origin)
	registered, err := oa.Register(h.ctx, oauth.RegisterInput{Name: "eval loaded agent", RedirectURIs: []string{"http://127.0.0.1:8787/callback"}, AuthMethod: "none"})
	if err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("v", 43)
	authorization := oauth.Authorization{ClientID: registered.ID, RedirectURI: registered.RedirectURIs[0], ResponseType: "code", Scope: "ledger:read", Challenge: oauth.Challenge(verifier), ChallengeMethod: "S256", Resource: origin + "/mcp"}
	code, err := oa.Authorize(h.ctx, authorization, h.owner)
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := oa.Exchange(h.ctx, registered.Client, code, authorization.RedirectURI, verifier, authorization.Resource)
	if err != nil {
		t.Fatal(err)
	}
	route, err := h.services.Previews.Issue(h.owner, "", preview.Target{Route: "/budget"})
	if err != nil {
		t.Fatal(err)
	}
	requests := func(ctx context.Context, path, body string, kind int) (*http.Response, error) {
		method := "GET"
		if body != "" {
			method = "POST"
		}
		req, err := http.NewRequestWithContext(ctx, method, h.endpoint+path, strings.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("MCP-Protocol-Version", "2026-07-28")
		req.Header.Set("Mcp-Method", "tools/call")
		name := "whoami"
		if strings.Contains(body, "dashboard_render") {
			name = "dashboard_render"
		}
		req.Header.Set("Mcp-Name", name)
		switch kind {
		case 0:
			req.AddCookie(&http.Cookie{Name: "__Host-moneybag_session", Value: h.cookie})
			req.Header.Set("Origin", origin)
			req.Header.Set("X-CSRF-Token", h.csrf)
		case 1:
			req.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
		case 2:
			req.AddCookie(&http.Cookie{Name: "moneybag_render", Value: route})
			req.Header.Set("X-CSRF-Token", "render")
		}
		return h.http.Do(req)
	}
	loadCtx, stopLoad := context.WithCancel(h.ctx)
	var load sync.WaitGroup
	var responses [600]atomic.Int64
	paths := []string{"/api/session", "/mcp", "/api/session", "/render/" + route}
	for worker := range 4 {
		load.Go(func() {
			for loadCtx.Err() == nil {
				body := ""
				if worker == 1 {
					body = mcpCall("whoami")
				}
				requestCtx, cancel := context.WithTimeout(loadCtx, 2*time.Second)
				resp, err := requests(requestCtx, paths[worker], body, worker%3)
				if err == nil {
					_, err = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
					if resp.StatusCode < len(responses) {
						responses[resp.StatusCode].Add(1)
					}
				}
				cancel()
				if err != nil {
					if loadCtx.Err() == nil {
						responses[0].Add(1)
					}
					return
				}
				select {
				case <-time.After(100 * time.Millisecond):
				case <-loadCtx.Done():
					return
				}
			}
		})
	}
	defer func() { stopLoad(); load.Wait() }()
	measurements := map[string]any{"scope": "Owned PostgreSQL 18, actual Fiber/compression/MCP and published SDK; simulated policy endpoint and capture", "workers": 4, "interval_ms": 100, "active_recheck_seconds": 25, "per_check_timeout_seconds": 2, "maximum_removal_ms": 60000}
	measurements["cancellation_layers"] = "Browser capture retains production guard; agent capture isolates HTTP/MCP cancellation by delegating only its caller context"
	for _, phase := range []struct {
		name  string
		state int32
	}{{"removal", 1}, {"policy_unavailable", 2}} {
		mode.Store(0)
		started, cancelled := make(chan struct{}, 2), make(chan struct{}, 2)
		capture := preview.Capture(func(ctx context.Context, _ string, _, _ int) ([]byte, error) {
			started <- struct{}{}
			<-ctx.Done()
			cancelled <- struct{}{}
			return nil, ctx.Err()
		})
		h.capture.Store(&capture)
		idle, releaseIdle, err := h.services.Identity.WatchAccess(h.ctx, h.owner.UserID)
		if err != nil {
			t.Fatal(err)
		}
		callBody := strings.Replace(mcpCall("dashboard_render"), `"arguments":{}`, `"arguments":{"id":"","route":"/budget"}`, 1)
		pending := make(chan error, 2)
		work, stopWork := context.WithTimeout(h.ctx, 30*time.Second)
		for kind := range 2 {
			go func() {
				path := "/api/mcp"
				if kind == 1 {
					path = "/mcp"
				}
				resp, err := requests(work, path, callBody, kind)
				if err == nil {
					_, err = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
					if err == nil {
						err = fmt.Errorf("MCP completed with HTTP %d", resp.StatusCode)
					}
				}
				pending <- err
			}()
		}
		for range 2 {
			select {
			case <-started:
			case early := <-pending:
				t.Fatal("MCP finished before capture setup", early)
			case <-time.After(5 * time.Second):
				t.Fatal("real MCP did not reach the owned capture")
			}
		}
		start := time.Now()
		mode.Store(phase.state)
		for range 2 {
			select {
			case <-cancelled:
			case <-work.Done():
				t.Fatal("capture exceeded the revocation bound")
			}
		}
		select {
		case <-idle.Done():
		case <-work.Done():
			t.Fatal("idle owner work exceeded the revocation bound")
		}
		measurements[phase.name+"_ms"] = time.Since(start).Milliseconds()
		for range 2 {
			select {
			case <-pending:
			case <-work.Done():
				t.Fatal("actual HTTP/MCP did not release canceled work")
			}
		}
		stopWork()
		releaseIdle()
	}
	mode.Store(0)
	if err = h.services.Identity.CheckAccess(h.ctx, h.owner.UserID, true); err != nil {
		t.Fatal("restoration failed", err)
	}
	stopLoad()
	load.Wait()
	if responses[0].Load() != 0 || responses[403].Load() == 0 || responses[503].Load() == 0 {
		t.Fatal("load missed a failure mode or had transport errors")
	}
	for status := range responses {
		if status != 0 && status != 200 && status != 403 && status != 503 && responses[status].Load() != 0 {
			t.Fatal("unexpected protected response", status)
		}
	}
	measurements["responses"] = map[string]int64{"allowed": responses[200].Load(), "denied": responses[403].Load(), "unavailable": responses[503].Load()}
	measurements["restoration"] = "pass"
	if target := os.Getenv("MONEYBAG_ACCESS_REHEARSAL_REPORT"); target != "" {
		data, err := json.MarshalIndent(measurements, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(target, append(data, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Log(measurements)
}
