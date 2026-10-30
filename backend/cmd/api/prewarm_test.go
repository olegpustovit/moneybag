package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

type warmFixture struct{ called chan string }

func (f warmFixture) List(context.Context, principal.Principal) ([]dashboard.Head, error) {
	return []dashboard.Head{{ID: "published", PublishedVersion: 1}, {ID: "draft"}}, nil
}
func (f warmFixture) Preview(_ context.Context, _ principal.Principal, id, _ string, _ map[string]any) (dashboard.Rendered, error) {
	f.called <- id
	return dashboard.Rendered{}, nil
}
func TestPrewarmDebouncesAndSkipsDrafts(t *testing.T) {
	cache, err := dashboard.NewSnapshots(audit.NewService(audit.NewMongoRepository(nil)))
	if err != nil {
		t.Fatal(err)
	}
	called := make(chan string, 3)
	change, stop := prewarmer(context.Background(), cache, warmFixture{called}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer stop()
	user := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	change(user, false)
	change(user, true)
	change(user, false)
	change(user, true)
	select {
	case id := <-called:
		if id != "published" {
			t.Fatalf("unexpected dashboard %q", id)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("prewarm did not run")
	}
	select {
	case id := <-called:
		t.Fatalf("duplicate or draft prewarm %q", id)
	case <-time.After(100 * time.Millisecond):
	}
}

func init() { prewarmDebounce = 100 * time.Millisecond }
