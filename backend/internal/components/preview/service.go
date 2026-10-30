package preview

import (
	"context"
	"crypto/rand"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/components/sharing"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/screenshot"
	"sync"
	"time"
)

type item struct {
	Owner, HTML, GrantID, GrantHash string
	Expires                         time.Time
}
type Service struct {
	mu         sync.Mutex
	items      map[string]item
	bytes      int
	url, style string
	renderer   *screenshot.Renderer
	sharing    *sharing.Service
}

func New(url, style string, r *screenshot.Renderer, shares *sharing.Service) *Service {
	return &Service{items: map[string]item{}, url: url, style: style, renderer: r, sharing: shares}
}
func (s *Service) Create(p principal.Principal, body string, grant *sharing.Grant) (string, string, error) {
	if e := p.Require("ledger:read"); e != nil {
		return "", "", e
	}
	html := `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; img-src data:"><title>Moneybag preview</title><style>` + s.style + `</style></head><body><main class="main">` + body + `</main></body></html>`
	if len(html) > 2*1024*1024 {
		return "", "", fmt.Errorf("preview exceeds 2 MiB")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for id, v := range s.items {
		if now.After(v.Expires) {
			delete(s.items, id)
			s.bytes -= len(v.HTML)
		}
	}
	if len(s.items) >= 128 || s.bytes+len(html) > 32*1024*1024 {
		return "", "", fmt.Errorf("preview cache is full; retry in ten minutes")
	}
	id := rand.Text()
	v := item{Owner: p.UserID, HTML: html, Expires: now.Add(10 * time.Minute)}
	if grant != nil {
		v.GrantID = grant.ID
		v.GrantHash = sharing.PermissionHash(*grant)
	}
	s.items[id] = v
	s.bytes += len(html)
	png := ""
	if s.renderer != nil && s.renderer.Binary != "" {
		png = s.url + "/previews/" + id + ".png"
	}
	return s.url + "/previews/" + id + ".html", png, nil
}
func (s *Service) PNG(ctx context.Context, html string) ([]byte, error) {
	if s.renderer == nil {
		return nil, fmt.Errorf("PNG renderer is unavailable")
	}
	return s.renderer.PNG(ctx, html)
}
func (s *Service) Read(ctx context.Context, p principal.Principal, id string, png bool) ([]byte, error) {
	if e := p.Require("ledger:read"); e != nil {
		return nil, e
	}
	s.mu.Lock()
	v, ok := s.items[id]
	s.mu.Unlock()
	if !ok || v.Owner != p.UserID || time.Now().After(v.Expires) {
		return nil, principal.ErrNotFound
	}
	if v.GrantID != "" {
		if s.sharing == nil {
			return nil, principal.ErrNotFound
		}
		g, e := s.sharing.Resolve(ctx, p, v.GrantID)
		if e != nil || sharing.PermissionHash(g) != v.GrantHash {
			return nil, principal.ErrNotFound
		}
	}
	if png {
		if s.renderer == nil {
			return nil, fmt.Errorf("PNG renderer unavailable")
		}
		return s.renderer.PNG(ctx, v.HTML)
	}
	return []byte(v.HTML), nil
}
