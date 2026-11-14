package integration

import (
	"context"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres/pgtest"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

func TestJournalDatePagination(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var err error
	p := principal.Principal{UserID: ulid.Make().String(), Scopes: []string{"ledger:read"}}
	repo := ledger.NewPostgresRepository(pgtest.New(t))
	if err = repo.Init(ctx, p); err != nil {
		t.Fatal(err)
	}
	service := ledger.NewService(repo, nil)
	dates := []string{"2026-01-03", "2026-01-01", "2026-01-02", "2026-01-01", "2026-01-03", "2026-01-02"}
	for i, date := range dates {
		_, err = insertJournal(ctx, pgtest.New(t), ledger.Entry{ID: fmt.Sprintf("e%d", i+1), UserID: p.UserID, Seq: int64(i + 1), Date: date, Status: "posted", Payee: "test"})
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = insertJournal(ctx, pgtest.New(t), ledger.Entry{ID: "other", UserID: ulid.Make().String(), Seq: 99, Date: "2026-01-01"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"e1", "e4"} {
		if _, err = insertTags(ctx, pgtest.New(t), p.UserID, id, []string{"selected"}); err != nil {
			t.Fatal(err)
		}
	}
	defaultPage, err := service.Entries(ctx, p, ledger.EntriesInput{Limit: 1})
	if err != nil || len(defaultPage.Items) != 1 || defaultPage.Items[0].Seq != 6 || defaultPage.NextCursor != 6 || defaultPage.NextCursorDate != "" {
		t.Fatal("default order", defaultPage, err)
	}
	tagged, err := service.Entries(ctx, p, ledger.EntriesInput{Sort: "date", Tag: "selected", Limit: 1})
	if err != nil || len(tagged.Items) != 1 || tagged.Items[0].Seq != 4 || tagged.NextCursorDate != "2026-01-01" {
		t.Fatal("tagged first page", tagged, err)
	}
	tagged, err = service.Entries(ctx, p, ledger.EntriesInput{Sort: "date", Tag: "selected", Limit: 1, Cursor: tagged.NextCursor, CursorDate: tagged.NextCursorDate})
	if err != nil || len(tagged.Items) != 1 || tagged.Items[0].Seq != 1 {
		t.Fatal("tagged next page", tagged, err)
	}
	for _, tc := range []struct {
		sort string
		want []int64
	}{{"date", []int64{2, 4, 3, 6, 1, 5}}, {"-date", []int64{5, 1, 6, 3, 4, 2}}} {
		in := ledger.EntriesInput{Sort: tc.sort, Limit: 2}
		var got []int64
		for {
			page, err := service.Entries(ctx, p, in)
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range page.Items {
				got = append(got, item.Seq)
			}
			if page.NextCursor == 0 {
				break
			}
			in.Cursor, in.CursorDate = page.NextCursor, page.NextCursorDate
		}
		if fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Fatalf("%s: got %v want %v", tc.sort, got, tc.want)
		}
	}
	page, err := service.Entries(ctx, p, ledger.EntriesInput{Sort: "date", From: "2026-01-02", To: "2026-01-02", Limit: 1})
	if err != nil || len(page.Items) != 1 || page.Items[0].Seq != 3 || page.NextCursorDate != "2026-01-02" {
		t.Fatal("date filter", page, err)
	}
	page, err = service.Entries(ctx, p, ledger.EntriesInput{Sort: "date", From: "2026-01-02", To: "2026-01-02", Limit: 1, Cursor: page.NextCursor, CursorDate: page.NextCursorDate})
	if err != nil || len(page.Items) != 1 || page.Items[0].Seq != 6 {
		t.Fatal("date cursor", page, err)
	}
	for _, in := range []ledger.EntriesInput{{Sort: "bad"}, {Sort: "date", Cursor: 1}, {CursorDate: "2026-01-01"}, {Sort: "date", Cursor: 1, CursorDate: "invalid"}} {
		if _, err = service.Entries(ctx, p, in); err == nil {
			t.Fatal("accepted invalid cursor", in)
		}
	}
}
