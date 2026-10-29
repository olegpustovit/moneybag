package integration

import (
	"context"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/budget"
	"github.com/olegpustovit/moneybag/backend/internal/components/identity"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/model"
	"github.com/olegpustovit/moneybag/backend/internal/components/rates"
	"github.com/olegpustovit/moneybag/backend/internal/components/tables"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/mongodb"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestScenarioLiveStartRefsAndIncomeReconcile(t *testing.T) {
	uri := os.Getenv("MONEYBAG_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("set MONEYBAG_TEST_MONGO_URI")
	}
	parsed, e := url.Parse(uri)
	if e != nil || !strings.HasPrefix(parsed.Path, "/moneybag_test_") {
		t.Fatal("integration tests require the dedicated moneybag_test_ database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	client, e := mongodb.Connect(ctx, uri)
	if e != nil {
		t.Fatal(e)
	}
	defer client.Disconnect(context.Background())
	db := client.Database(strings.TrimPrefix(parsed.Path, "/") + "_live")
	_ = db.Drop(ctx)
	defer db.Drop(context.Background())
	ids := identity.NewService(identity.NewMongoRepository(db))
	if e = ids.Init(ctx); e != nil {
		t.Fatal(e)
	}
	u, e := ids.Setup(ctx, "live@example.test", "Live", "synthetic-test-password")
	if e != nil {
		t.Fatal(e)
	}
	p := identity.Principal(u)
	a := audit.NewService(audit.NewMongoRepository(db))
	repo := ledger.NewMongoRepository(db)
	if e = repo.Init(ctx, p); e != nil {
		t.Fatal(e)
	}
	book := ledger.NewService(repo, a)
	expr, _ := expressions.New()
	prices := rates.NewService(rates.NewMongoRepository(db), a)
	budgets := budget.NewService(budget.NewMongoRepository(db), a, prices)
	models := model.NewService(model.NewMongoRepository(db), tables.NewService(tables.NewMongoRepository(db), a, expr), a, expr)
	models.Ledger, models.Rates, models.Budget = book, prices, budgets
	for _, acc := range []ledger.Account{{ID: "assets:w", Type: "asset", Name: "W", Pot: "wise_usd", Commodities: []string{"USD"}}, {ID: "assets:k", Type: "asset", Name: "K", Pot: "cash_kyiv", Commodities: []string{"USD"}}, {ID: "equity:open", Type: "equity", Name: "Open", Commodities: []string{"USD"}}, {ID: "income:pay", Type: "income", Name: "Pay", Commodities: []string{"USD"}}} {
		if _, e = book.CreateAccount(ctx, p, acc); e != nil {
			t.Fatal(e)
		}
	}
	post := func(date, payee, to, from, amt string) {
		v := ledger.MustDecimal(amt)
		if _, e := book.Post(ctx, p, ledger.EntryInput{Date: date, Payee: payee, Commit: true, Postings: []ledger.Posting{{Account: to, Amount: ledger.Amount{Value: v, Commodity: "USD"}}, {Account: from, Amount: ledger.Amount{Value: v.Negate(), Commodity: "USD"}}}}); e != nil {
			t.Fatal(e)
		}
	}
	post("2026-10-01", "Opening", "assets:w", "equity:open", "1000")
	post("2026-10-01", "Opening", "assets:k", "equity:open", "500")
	if _, e = prices.Set(ctx, p, rates.SetInput{Pair: "EUR/USD", Value: ledger.MustDecimal("1.5"), At: "2026-10-01T00:00:00Z", Source: "test"}); e != nil {
		t.Fatal(e)
	}
	if e = models.Initialize(ctx, p); e != nil {
		t.Fatal(e)
	}
	half := 0.5
	if _, e = models.Upsert(ctx, p, model.Scenario{ID: "refs", Name: "Refs", Overrides: []model.Override{{Assumption: "spend_eur", Ref: "budget_lines.total", Factor: &half}}}); e != nil {
		t.Fatal(e)
	}
	lim := ledger.MustDecimal("100")
	if _, e = budgets.Set(ctx, p, budget.SetInput{ID: "rent", Limit: &lim}); e != nil {
		t.Fatal(e)
	}
	run := func() model.Projection {
		out, e := models.Forecast(ctx, p, model.ForecastInput{ScenarioIDs: []string{"base"}, HorizonMonths: 1, AsOf: "2026-10-31"})
		if e != nil {
			t.Fatal(e)
		}
		return out[0]
	}
	before := run()
	if before.StartNetUSD != 1500 || before.StartSource != "ledger" || before.JournalSeq < 2 || len(before.RatesUsed) != 1 || before.Months[0].Wise != 1000 || before.Months[0].Kyiv != 500 {
		t.Fatal("live start not read from the ledger", before)
	}
	post("2026-10-02", "Top up", "assets:w", "equity:open", "100")
	if after := run(); after.EndNetUSD != before.EndNetUSD+100 {
		t.Fatal("posting did not move the forecast", after.EndNetUSD, before.EndNetUSD)
	}
	refs, e := models.Forecast(ctx, p, model.ForecastInput{ScenarioIDs: []string{"refs"}, HorizonMonths: 1, AsOf: "2026-10-31"})
	if e != nil || refs[0].SpendUSD != 75 || len(refs[0].Inputs) != 1 || refs[0].Inputs[0].Value != 50 { // 100 EUR * 0.5 = 50 EUR; spend 50 * 1.5 USD
		t.Fatal("ref not resolved", refs, e)
	}
	if _, e = models.Compare(ctx, p, model.CompareInput{ScenarioIDs: []string{"base"}, Metrics: []string{"nope"}}); e == nil || !strings.Contains(e.Error(), "runway_ex_gold") {
		t.Fatal("unknown metric must list valid ones", e)
	}
	if _, e = models.SetIncome(ctx, p, []model.Income{{ID: "pay", Amount: 100, Currency: "USD", Frequency: "monthly", Kind: "salary", PayerMatch: `row.payee == "Acme"`, LandsIn: "assets:w"}}); e != nil {
		t.Fatal(e)
	}
	post("2026-10-05", "Acme", "assets:w", "income:pay", "80")
	post("2026-10-06", "Other", "assets:w", "income:pay", "30")
	rec, e := models.IncomeReconcile(ctx, p, model.ReconcileInput{StreamID: "pay", From: "2026-10-01", To: "2026-10-31"})
	if e != nil || len(rec) != 1 || rec[0].Actual != 80 || rec[0].Variance != -20 || len(rec[0].EntryIDs) != 1 {
		t.Fatal("income reconcile", rec, e)
	}
}
