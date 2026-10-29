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

func TestJournalDatePagination(t *testing.T) {
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
	db := client.Database(strings.TrimPrefix(parsed.Path, "/") + "_date_sort")
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
	dates := []string{"2026-01-03", "2026-01-01", "2026-01-02", "2026-01-01", "2026-01-03", "2026-01-02"}
	for i, date := range dates {
		_, err = db.Collection("journal").InsertOne(ctx, bson.M{"_id": fmt.Sprintf("e%d", i+1), "user_id": p.UserID, "seq": i + 1, "date": date, "status": "posted", "payee": "test"})
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = db.Collection("journal").InsertOne(ctx, bson.M{"_id": "other", "user_id": ulid.Make().String(), "seq": 99, "date": "2026-01-01"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"e1", "e4"} {
		if _, err = db.Collection("entry_tags").InsertOne(ctx, bson.M{"_id": p.UserID + ":" + id, "user_id": p.UserID, "entry_id": id, "tags": bson.A{"selected"}}); err != nil {
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
