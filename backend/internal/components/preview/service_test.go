package preview

import (
	"context"
	"github.com/oklog/ulid/v2"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"strings"
	"testing"
	"time"
)

func TestPrivatePreviewExpiryAndLimits(t *testing.T) {
	p := principal.Principal{UserID: ulid.Make().String(), Scopes: []string{"ledger:read"}}
	s := New("http://127.0.0.1:6459", "body{color:black}", nil, nil)
	html, png, e := s.Create(p, "<p>Safe output</p>", nil)
	if e != nil || png != "" {
		t.Fatal(html, png, e)
	}
	id := strings.TrimSuffix(strings.TrimPrefix(html, "http://127.0.0.1:6459/previews/"), ".html")
	b, e := s.Read(context.Background(), p, id, false)
	if e != nil || !strings.Contains(string(b), "Safe output") {
		t.Fatal(e)
	}
	other := p
	other.UserID = ulid.Make().String()
	if _, e = s.Read(context.Background(), other, id, false); e != principal.ErrNotFound {
		t.Fatal("cross-user preview", e)
	}
	v := s.items[id]
	v.Expires = time.Now().Add(-time.Second)
	s.items[id] = v
	if _, e = s.Read(context.Background(), p, id, false); e != principal.ErrNotFound {
		t.Fatal("expired preview", e)
	}
	if _, _, e = s.Create(p, strings.Repeat("x", 2*1024*1024), nil); e == nil {
		t.Fatal("oversize preview accepted")
	}
}
