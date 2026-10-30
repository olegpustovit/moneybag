package preview

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

func testPrincipal() principal.Principal {
	return principal.Principal{UserID: ulid.Make().String(), Scopes: []string{"ledger:read"}}
}

func TestTokenExpiryAndScope(t *testing.T) {
	s := New("http://127.0.0.1:6459", nil)
	p := testPrincipal()
	target := Target{Dashboard: "cash", Version: "7", Params: map[string]any{"month": "2026-10"}, Width: 800}
	token, err := s.Issue(p, "grant-hash", target)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Resolve(token)
	if err != nil || got.Principal.UserID != p.UserID || got.GrantHash != "grant-hash" || got.Target.Dashboard != "cash" || got.Target.Version != "7" || got.Target.Params["month"] != "2026-10" {
		t.Fatal("binding", got, err)
	}
	if got.Expires.After(time.Now().Add(tokenTTL)) || got.Expires.Before(time.Now().Add(tokenTTL-time.Second)) {
		t.Fatal("ttl", got.Expires)
	}
	other, err := s.Issue(testPrincipal(), "", Target{Dashboard: "other"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Report(other, Report{TimingMS: 5}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.take(token); ok {
		t.Fatal("a report reached another token")
	}
	s.sessions[token].Expires = time.Now().Add(-time.Millisecond)
	if _, err = s.Resolve(token); !errors.Is(err, principal.ErrNotFound) {
		t.Fatal("expired token resolved", err)
	}
	if err = s.Report(token, Report{}); !errors.Is(err, principal.ErrNotFound) {
		t.Fatal("expired token accepted a report", err)
	}
	if _, err = s.Resolve("unknown"); !errors.Is(err, principal.ErrNotFound) {
		t.Fatal("unknown token resolved", err)
	}
	if _, err = s.Issue(principal.Principal{UserID: p.UserID, Scopes: []string{"ledger:write"}}, "", target); err == nil {
		t.Fatal("token issued without ledger:read")
	}
	if err = s.Report(other, Report{Layout: make([]Box, maxReportRows+1)}); err == nil {
		t.Fatal("oversized report accepted")
	}
}

func TestRenderRequiresReportAndRetakesTallPages(t *testing.T) {
	p := testPrincipal()
	var s *Service
	var heights []int
	report := func(height float64) Capture {
		return func(_ context.Context, url string, width, h int) ([]byte, error) {
			heights = append(heights, h)
			if width != 800 {
				t.Fatal("width", width)
			}
			token := strings.TrimPrefix(url, "http://127.0.0.1:6459/render/")
			if err := s.Report(token, Report{Layout: []Box{{Component: "a", H: height}}, Checks: Checks{Overflow: []string{"a"}}, TimingMS: 12}); err != nil {
				t.Fatal(err)
			}
			return []byte("png"), nil
		}
	}
	s = New("http://127.0.0.1:6459", report(2000))
	result, err := s.Render(context.Background(), p, "", Target{Dashboard: "cash", Width: 800})
	if err != nil || string(result.PNG) != "png" || result.Report.TimingMS != 12 || len(heights) != 2 || heights[0] != firstHeight || heights[1] != 2000 {
		t.Fatal(result, heights, err)
	}
	if len(s.sessions) != 0 {
		t.Fatal("token outlived its render")
	}
	failures, err := s.Gate(context.Background(), p, "", Target{Dashboard: "cash", Width: 800})
	if err != nil || len(failures) != 1 || !strings.Contains(failures[0], "overflows") {
		t.Fatal("gate", failures, err)
	}
	silent := New("http://127.0.0.1:6459", func(context.Context, string, int, int) ([]byte, error) { return []byte("blank"), nil })
	if _, err = silent.Render(context.Background(), p, "", Target{Dashboard: "cash"}); !errors.Is(err, ErrNoReport) {
		t.Fatal("blank page passed as success", err)
	}
	if _, err = silent.Gate(context.Background(), p, "", Target{Dashboard: "cash"}); !errors.Is(err, ErrNoReport) {
		t.Fatal("gate passed a blank page", err)
	}
	if failures, err = New("x", nil).Gate(context.Background(), p, "", Target{}); err != nil || failures != nil {
		t.Fatal("gate without a renderer", failures, err)
	}
}
