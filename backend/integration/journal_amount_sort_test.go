package integration

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/mongodb"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestJournalAmountPagination(t *testing.T) {
	uri := os.Getenv("MONEYBAG_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("set MONEYBAG_TEST_MONGO_URI")
	}
	parsed, err := url.Parse(uri)
	if err != nil || !strings.HasPrefix(parsed.Path, "/moneybag_test_") {
		t.Fatal("dedicated test database required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	client, err := mongodb.Connect(ctx, uri)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect(context.Background())
	db := client.Database(strings.TrimPrefix(parsed.Path, "/") + "_amount_sort")
	if err = db.Drop(ctx); err != nil {
		t.Fatal(err)
	}
	defer db.Drop(context.Background())
	p := principal.Principal{UserID: ulid.Make().String(), Scopes: []string{"ledger:read"}}
	repo := ledger.NewMongoRepository(db)
	if err = repo.Init(ctx, p); err != nil {
		t.Fatal(err)
	}
	service := ledger.NewService(repo, nil)
	vals := []string{"-2", "1.01", "1", "1.0000000000000000001", "1"}
	for i, value := range vals {
		postings := []ledger.Posting{{Account: "assets:cash", Amount: ledger.Amount{Value: ledger.MustDecimal(value), Commodity: "EUR"}}, {Account: "expenses:food", Amount: ledger.Amount{Value: ledger.MustDecimal(fmt.Sprint(10 - i)), Commodity: "USD"}}}
		_, err = db.Collection("journal").InsertOne(ctx, bson.M{"_id": fmt.Sprintf("e%d", i+1), "user_id": p.UserID, "seq": i + 1, "date": "2026-01-01", "status": "posted", "postings": postings})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Collection("journal").InsertOne(ctx, bson.M{"_id": "empty", "user_id": p.UserID, "seq": 6, "date": "2026-01-01", "status": "draft"}); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Collection("journal").InsertOne(ctx, bson.M{"_id": "other", "user_id": ulid.Make().String(), "seq": 99, "date": "2026-01-01"}); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Collection("entry_tags").InsertOne(ctx, bson.M{"_id": p.UserID + ":e3", "user_id": p.UserID, "entry_id": "e3", "tags": bson.A{"selected"}}); err != nil {
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
