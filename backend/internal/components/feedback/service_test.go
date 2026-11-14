package feedback

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres/pgtest"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

func TestSavedViewsStayPrivate(t *testing.T) {
	service := NewService(NewPostgresRepository(pgtest.New(t)), nil)
	ctx := context.Background()
	owner := principal.Principal{UserID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Scopes: []string{"ledger:read"}}
	other := owner
	other.UserID = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	view, err := service.SaveView(ctx, owner, SavedView{ID: "same", Name: "Original", Page: "/ledger", State: json.RawMessage(`{"from":"2026-01-01"}`)})
	if err != nil {
		t.Fatal(err)
	}
	view.Name = "Updated"
	if _, err := service.SaveView(ctx, owner, view); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveView(ctx, other, SavedView{ID: "same", Name: "Other", Page: "/ledger"}); err != nil {
		t.Fatal(err)
	}
	views, err := service.SavedViews(ctx, owner)
	if err != nil || len(views) != 1 || views[0].Name != "Updated" || views[0].Key != owner.UserID+":same" {
		t.Fatalf("views: %+v %v", views, err)
	}
	var state map[string]string
	if err := json.Unmarshal(views[0].State, &state); err != nil || state["from"] != "2026-01-01" {
		t.Fatalf("state: %s %v", views[0].State, err)
	}
	if err := service.DeleteView(ctx, other, "same"); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteView(ctx, other, "same"); !errors.Is(err, principal.ErrNotFound) {
		t.Fatal(err)
	}
	views, err = service.SavedViews(ctx, owner)
	if err != nil || len(views) != 1 {
		t.Fatalf("other user removed owner's view: %+v %v", views, err)
	}
	if err := service.DeleteView(ctx, owner, "same"); err != nil {
		t.Fatal(err)
	}
	views, err = service.SavedViews(ctx, owner)
	if err != nil || views == nil || len(views) != 0 {
		t.Fatalf("empty views: %+v %v", views, err)
	}
}

func TestConcurrentUsageAndOwnerIsolation(t *testing.T) {
	service := NewService(NewPostgresRepository(pgtest.New(t)), nil)
	ctx := context.Background()
	p := principal.Principal{UserID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Scopes: []string{"ledger:read"}}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if err := service.Record(ctx, p, UsageBeacon{Path: "/ledger?from=2026-01-01"}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	report, err := service.Usage(ctx, p, UsageInput{})
	if err != nil || len(report.Pages) != 1 || report.Pages[0].Views != 20 || report.Pages[0].Path != "/ledger" || report.Pages[0].LastAt == "" {
		t.Fatalf("usage: %+v %v", report, err)
	}
	p.UserID = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	report, err = service.Usage(ctx, p, UsageInput{})
	if err != nil || report.Pages == nil || len(report.Pages) != 0 {
		t.Fatalf("cross-owner usage: %+v %v", report, err)
	}
}
