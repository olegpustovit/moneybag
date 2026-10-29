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
	"github.com/olegpustovit/moneybag/backend/internal/components/budget"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/mongodb"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestBudgetMonthlyPrecision(t *testing.T) {
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
	db := client.Database(strings.TrimPrefix(parsed.Path, "/") + "_budget_precision")
	if err = db.Drop(ctx); err != nil {
		t.Fatal(err)
	}
	defer db.Drop(context.Background())
	p := principal.Principal{UserID: ulid.Make().String(), Scopes: []string{"ledger:read"}}
	_, err = db.Collection("budgets").InsertOne(ctx, bson.M{"_id": p.UserID + ":food", "user_id": p.UserID, "id": "food", "accounts": bson.A{"expenses:food"}})
	if err != nil {
		t.Fatal(err)
	}
	values := []struct{ account, value, commodity, price string }{
		{"expenses:food", "10000000000000000", "EUR", ""},
		{"expenses:food", "1", "EUR", ""},
		{"expenses:food", "-10000000000000000", "EUR", ""},
		{"expenses:food", "3", "USD", "2"},
		{"expenses:other", "2", "EUR", ""},
	}
	for i, v := range values {
		posting := ledger.Posting{Account: v.account, Amount: ledger.Amount{Value: ledger.MustDecimal(v.value), Commodity: v.commodity}}
		if v.price != "" {
			posting.Price = &ledger.Amount{Value: ledger.MustDecimal(v.price), Commodity: "EUR"}
		}
		_, err = db.Collection("journal").InsertOne(ctx, bson.M{"user_id": p.UserID, "date": fmt.Sprintf("2026-01-%02d", i+1), "status": "posted", "postings": []ledger.Posting{posting}})
		if err != nil {
			t.Fatal(err)
		}
	}
	for i, value := range []string{"0.1", "0.2"} {
		posting := ledger.Posting{Account: "expenses:food", Amount: ledger.Amount{Value: ledger.MustDecimal(value), Commodity: "EUR"}}
		_, err = db.Collection("journal").InsertOne(ctx, bson.M{"user_id": p.UserID, "date": fmt.Sprintf("2026-02-%02d", i+1), "status": "posted", "postings": []ledger.Posting{posting}})
		if err != nil {
			t.Fatal(err)
		}
	}
	service := budget.NewService(budget.NewMongoRepository(db), nil, nil)
	for i := 0; i < 20; i++ {
		months, err := service.Months(ctx, p, "2026-01-01", "2026-02-28")
		if err != nil || len(months) != 2 || months[0].ByLine["food"] != 7 || months[0].ByLine["unmapped"] != 2 || months[0].Total != 9 || months[1].ByLine["food"] != 0.3 || months[1].Total != 0.3 {
			t.Fatalf("monthly totals: %+v, %v", months, err)
		}
	}
}
