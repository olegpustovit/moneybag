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

func TestJournalAmountPagination(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var err error
	p := principal.Principal{UserID: ulid.Make().String(), Scopes: []string{"ledger:read"}}
	repo := ledger.NewPostgresRepository(pgtest.New(t))
	if err = repo.Init(ctx, p); err != nil {
		t.Fatal(err)
	}
	service := ledger.NewService(repo, nil)
	vals := []string{"-2", "1.01", "1", "1.0000000000000000001", "1"}
	for i, value := range vals {
		postings := []ledger.Posting{{Account: "assets:cash", Amount: ledger.Amount{Value: ledger.MustDecimal(value), Commodity: "EUR"}}, {Account: "expenses:food", Amount: ledger.Amount{Value: ledger.MustDecimal(fmt.Sprint(10 - i)), Commodity: "USD"}}}
		_, err = insertJournal(ctx, pgtest.New(t), ledger.Entry{ID: fmt.Sprintf("e%d", i+1), UserID: p.UserID, Seq: int64(i + 1), Date: "2026-01-01", Status: "posted", Postings: postings})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err = insertJournal(ctx, pgtest.New(t), ledger.Entry{ID: "empty", UserID: p.UserID, Seq: 6, Date: "2026-01-01", Status: "draft"}); err != nil {
		t.Fatal(err)
	}
	if _, err = insertJournal(ctx, pgtest.New(t), ledger.Entry{ID: "other", UserID: ulid.Make().String(), Seq: 99, Date: "2026-01-01"}); err != nil {
		t.Fatal(err)
	}
	if _, err = insertTags(ctx, pgtest.New(t), p.UserID, "e3", []string{"selected"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		sort, account, tag string
		accounts           []string
		want               []int64
	}{
		{"amount", "", "", nil, []int64{6, 1, 3, 5, 4, 2}}, {"-amount", "", "", nil, []int64{2, 4, 5, 3, 1, 6}},
		{"amount", "expenses:food", "", nil, []int64{5, 4, 3, 2, 1}}, {"-amount", "expenses:food", "", nil, []int64{1, 2, 3, 4, 5}},
		{"amount", "", "selected", nil, []int64{3}},
		{"amount", "", "", []string{"expenses:food", "income:salary"}, []int64{5, 4, 3, 2, 1}},
		{"-amount", "", "", []string{"assets:cash", "expenses:food"}, []int64{2, 4, 5, 3, 1}},
	} {
		in := ledger.EntriesInput{Sort: tc.sort, Account: tc.account, Accounts: tc.accounts, Tag: tc.tag, Limit: 1}
		var got []int64
		for {
			page, err := service.Entries(ctx, p, in)
			if err != nil {
				t.Fatalf("%+v: %v", tc, err)
			}
			for _, item := range page.Items {
				got = append(got, item.Seq)
			}
			if page.NextCursor == 0 {
				break
			}
			in.Cursor, in.CursorAmount = page.NextCursor, page.NextCursorAmount
		}
		if fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Fatalf("%+v: got %v", tc, got)
		}
	}
	for _, in := range []ledger.EntriesInput{{Account: "assets:cash", Accounts: []string{"expenses:food"}}, {Accounts: []string{"invalid"}}, {Sort: "amount", Cursor: 1}, {Sort: "amount", Cursor: 1, CursorAmount: "NaN"}, {Sort: "date", Cursor: 1, CursorAmount: "1"}, {CursorAmount: "1"}} {
		if _, err = service.Entries(ctx, p, in); err == nil {
			t.Fatalf("accepted invalid cursor %+v", in)
		}
	}
}
