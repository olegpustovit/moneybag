package integration

import (
	"context"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type historyValuer struct{}

func (historyValuer) Convert(_ context.Context, _ principal.Principal, a ledger.Amount, target, _ string) (ledger.Amount, error) {
	a.Commodity = target
	return a, nil
}

func TestBalanceHistoryMatchesSequentialBalances(t *testing.T) {
	uri := os.Getenv("MONEYBAG_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("set MONEYBAG_TEST_MONGO_URI")
	}
	parsed, err := url.Parse(uri)
	if err != nil || !strings.HasPrefix(parsed.Path, "/moneybag_test_") {
		t.Fatal("dedicated test database required")
	}
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db := client.Database(strings.TrimPrefix(parsed.Path, "/") + "_history")
	if err := db.Drop(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Drop(ctx); _ = client.Disconnect(ctx) })
	p := principal.Principal{UserID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Scopes: []string{"ledger:read"}}
	accounts := []any{ledger.Account{Key: "a", UserID: p.UserID, ID: "assets:a", Type: "asset"}, ledger.Account{Key: "l", UserID: p.UserID, ID: "liabilities:l", Type: "liability"}}
	if _, err := db.Collection("accounts").InsertMany(ctx, accounts); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	entries := []any{}
	for i := 0; i < 12; i++ {
		entries = append(entries, ledger.Entry{ID: time.Date(now.Year(), now.Month()-time.Month(i), 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02"), UserID: p.UserID, Status: "posted", Date: time.Date(now.Year(), now.Month()-time.Month(i), 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02"), Postings: []ledger.Posting{{Account: "assets:a", Amount: ledger.Amount{Value: ledger.MustDecimal("10.25"), Commodity: "USD"}}, {Account: "liabilities:l", Amount: ledger.Amount{Value: ledger.MustDecimal("-3.50"), Commodity: "EUR"}}}})
	}
	if _, err := db.Collection("journal").InsertMany(ctx, entries); err != nil {
		t.Fatal(err)
	}
	book := ledger.NewService(ledger.NewMongoRepository(db), nil)
	valuer := historyValuer{}
	ctx = ledger.WithAccountCache(ctx)
	got, err := (&dashboard.Sources{Ledger: book, Valuer: valuer}).Rows(ctx, p, "balance_history")
	if err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{}
	for i := 11; i >= 0; i-- {
		end := time.Date(now.Year(), now.Month()-time.Month(i)+1, 0, 0, 0, 0, 0, time.UTC)
		if i == 0 {
			end = now
		}
		res, err := book.Balances(ctx, p, ledger.BalancesInput{At: end.Format("2006-01-02"), GroupBy: "type", ValueIn: "USD"}, valuer)
		if err != nil {
			t.Fatal(err)
		}
		assets, liabilities := 0.0, 0.0
		for _, row := range res.Rows {
			if row.Value != nil {
				switch row.Key {
				case "asset":
					assets = row.Value.Value.Float()
				case "liability":
					liabilities = row.Value.Value.Float()
				}
			}
		}
		want = append(want, map[string]any{"month": end.Format("2006-01"), "date": end.Format("2006-01-02"), "assets": assets, "liabilities": liabilities, "net": assets + liabilities})
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parallel history differs from sequential: got %v, want %v", got, want)
	}
}
