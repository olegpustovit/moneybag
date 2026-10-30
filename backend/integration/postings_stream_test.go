package integration

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/reqcache"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type testValuer struct{}

func (testValuer) Convert(_ context.Context, _ principal.Principal, a ledger.Amount, target, _ string) (ledger.Amount, error) {
	return ledger.Amount{Value: a.Value, Commodity: target}, nil
}

func postingsFixture(tb testing.TB) (context.Context, principal.Principal, *dashboard.Sources, *atomic.Int64) {
	tb.Helper()
	uri := os.Getenv("MONEYBAG_TEST_MONGO_URI")
	if uri == "" {
		tb.Skip("set MONEYBAG_TEST_MONGO_URI")
	}
	parsed, err := url.Parse(uri)
	if err != nil || !strings.HasPrefix(parsed.Path, "/moneybag_test_") {
		tb.Fatal("dedicated test database required")
	}
	dbName := strings.TrimPrefix(parsed.Path, "/") + "_postings"
	var reads atomic.Int64
	monitor := &event.CommandMonitor{Started: func(_ context.Context, e *event.CommandStartedEvent) {
		if e.CommandName == "aggregate" || e.CommandName == "find" || e.CommandName == "getMore" {
			reads.Add(1)
		}
	}}
	client, err := mongo.Connect(options.Client().ApplyURI(uri).SetMonitor(monitor))
	if err != nil {
		tb.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	tb.Cleanup(cancel)
	db := client.Database(dbName)
	if err := db.Drop(ctx); err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = db.Drop(context.Background()); _ = client.Disconnect(context.Background()) })
	p := principal.Principal{UserID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Scopes: []string{"ledger:read"}}
	repo := ledger.NewMongoRepository(db)
	if err := repo.Init(ctx, p); err != nil {
		tb.Fatal(err)
	}
	if _, err := db.Collection("accounts").InsertOne(ctx, ledger.Account{Key: p.UserID + ":assets:test", UserID: p.UserID, ID: "assets:test", Type: "asset", Class: "bank", Name: "Test", Commodities: []string{"EUR"}}); err != nil {
		tb.Fatal(err)
	}
	docs := make([]any, 20000)
	for i := range docs {
		docs[i] = ledger.Entry{UserID: p.UserID, ID: fmt.Sprintf("entry-%05d", i), Seq: int64(i + 1), Date: "2026-01-01", Kind: "entry", Status: "posted", Postings: []ledger.Posting{{Account: "assets:test", Amount: ledger.Amount{Value: ledger.MustDecimal("1"), Commodity: "EUR"}}}}
	}
	if _, err := db.Collection("journal").InsertMany(ctx, docs); err != nil {
		tb.Fatal(err)
	}
	reversals := []any{
		ledger.Entry{UserID: p.UserID, ID: "rev-1", Seq: 20001, Date: "2026-01-01", Kind: "reversal", Status: "posted", Links: []ledger.Link{{Rel: "reverses", ID: "entry-00000"}}},
		ledger.Entry{UserID: p.UserID, ID: "rev-2", Seq: 20002, Date: "2026-01-01", Kind: "reversal", Status: "posted", Links: []ledger.Link{{Rel: "reverses", ID: "rev-1"}}},
		ledger.Entry{UserID: p.UserID, ID: "rev-3", Seq: 20003, Date: "2026-01-01", Kind: "reversal", Status: "posted", Links: []ledger.Link{{Rel: "reverses", ID: "entry-00001"}}},
		ledger.Entry{UserID: "01ARZ3NDEKTSV4RRFFQ69G5FAW", ID: "other-user", Seq: 1, Date: "2026-01-01", Kind: "entry", Status: "posted", Postings: []ledger.Posting{{Account: "assets:test", Amount: ledger.Amount{Value: ledger.MustDecimal("1"), Commodity: "EUR"}}}},
	}
	if _, err := db.Collection("journal").InsertMany(ctx, reversals); err != nil {
		tb.Fatal(err)
	}
	if _, err := db.Collection("entry_tags").InsertOne(ctx, bson.M{"_id": p.UserID + ":entry-00000", "user_id": p.UserID, "entry_id": "entry-00000", "tags": []string{"synthetic"}}); err != nil {
		tb.Fatal(err)
	}
	sources := &dashboard.Sources{Ledger: ledger.NewService(repo, nil), Valuer: testValuer{}}
	return ctx, p, sources, &reads
}

func TestPostingsStreamLargeJournal(t *testing.T) {
	ctx, p, sources, reads := postingsFixture(t)
	reads.Store(0)
	started := time.Now()
	rows, err := sources.Rows(reqcache.With(ctx), p, "postings")
	if err != nil || len(rows) != 19999 {
		t.Fatalf("postings: %d rows, %v", len(rows), err)
	}
	if rows[0]["seq"] != float64(20000) || rows[len(rows)-1]["seq"] != float64(1) {
		t.Fatal("journal sequence order changed")
	}
	if n := reads.Load(); n > 4 {
		t.Fatalf("20,000 entries required %d reads; want at most three journal reads and one accounts read", n)
	}
	seen := false
	for _, row := range rows {
		if row["entry"] == "entry-00001" || row["entry"] == "other-user" {
			t.Fatalf("unexpected entry: %v", row["entry"])
		}
		if row["entry"] == "entry-00000" {
			seen = true
			if tags := row["tags"].([]any); len(tags) != 1 || tags[0] != "synthetic" {
				t.Fatalf("tag overlay: %v", tags)
			}
		}
	}
	if !seen {
		t.Fatal("even reversal chain should retain original")
	}
	t.Logf("20,000 entries in %s; %d read commands including accounts and cursor batches", time.Since(started), reads.Load())
}

func BenchmarkPostingsSource(b *testing.B) {
	ctx, p, sources, _ := postingsFixture(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := sources.Rows(reqcache.With(ctx), p, "postings")
		if err != nil || len(rows) != 19999 {
			b.Fatalf("postings: %d rows, %v", len(rows), err)
		}
	}
}

type historyValuer struct{}

func (historyValuer) Convert(_ context.Context, _ principal.Principal, a ledger.Amount, target, at string) (ledger.Amount, error) {
	factor := big.NewRat(1, 1)
	if a.Commodity == "EUR" {
		factor = big.NewRat(5, 4)
		if at >= "2026-02-01" {
			factor = big.NewRat(3, 2)
		}
	}
	value, err := ledger.DecimalFromRat(new(big.Rat).Mul(a.Value.Rat(), factor))
	return ledger.Amount{Value: value, Commodity: target}, err
}
func TestBalanceSeriesMatchesIndividualDates(t *testing.T) {
	ctx, p, sources, reads := postingsFixture(t)
	uri := os.Getenv("MONEYBAG_TEST_MONGO_URI")
	parsed, _ := url.Parse(uri)
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect(context.Background())
	db := client.Database(strings.TrimPrefix(parsed.Path, "/") + "_postings")
	if _, err = db.Collection("accounts").InsertOne(ctx, ledger.Account{Key: p.UserID + ":liabilities:test", UserID: p.UserID, ID: "liabilities:test", Type: "liability", Name: "Loan", Commodities: []string{"USD"}}); err != nil {
		t.Fatal(err)
	}
	docs := []any{ledger.Entry{UserID: p.UserID, ID: "later", Seq: 20010, Date: "2026-02-15", Status: "posted", Postings: []ledger.Posting{{Account: "assets:test", Amount: ledger.Amount{Value: ledger.MustDecimal("0.123456"), Commodity: "EUR"}}, {Account: "liabilities:test", Amount: ledger.Amount{Value: ledger.MustDecimal("-42.75"), Commodity: "USD"}}}}, ledger.Entry{UserID: p.UserID, ID: "future", Seq: 20011, Date: "2027-01-01", Status: "posted", Postings: []ledger.Posting{{Account: "assets:test", Amount: ledger.Amount{Value: ledger.MustDecimal("100"), Commodity: "EUR"}}}}, ledger.Entry{UserID: p.UserID, ID: "draft", Seq: 20012, Date: "2026-01-01", Status: "draft", Postings: []ledger.Posting{{Account: "assets:test", Amount: ledger.Amount{Value: ledger.MustDecimal("500"), Commodity: "EUR"}}}}}
	if _, err = db.Collection("journal").InsertMany(ctx, docs); err != nil {
		t.Fatal(err)
	}
	dates := []string{"2025-12-31", "2026-01-01", "2026-02-28", "2026-03-31", "2026-04-30", "2026-05-31", "2026-06-30", "2026-07-31", "2026-08-31", "2026-09-30", "2026-10-31", "2026-12-31"}
	want := make([]ledger.BalancesResult, len(dates))
	for i, date := range dates {
		want[i], err = sources.Ledger.Balances(ctx, p, ledger.BalancesInput{At: date, GroupBy: "type", ValueIn: "USD"}, historyValuer{})
		if err != nil {
			t.Fatal(err)
		}
	}
	reads.Store(0)
	got, err := sources.Ledger.BalanceSeries(ctx, p, dates, historyValuer{})
	if err != nil {
		t.Fatal(err)
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("series %s != individual balances %s", gotJSON, wantJSON)
	}
	if n := reads.Load(); n > 3 {
		t.Fatalf("series required %d reads", n)
	}
	if _, err = sources.Ledger.BalanceSeries(ctx, p, []string{"2026-01-01", "2025-12-31"}, historyValuer{}); err == nil {
		t.Fatal("unsorted dates accepted")
	}
}

func TestBalanceReadMemoIsolation(t *testing.T) {
	ctx, p, sources, reads := postingsFixture(t)
	ctx = reqcache.With(ctx)
	reads.Store(0)
	in := ledger.BalancesInput{At: "2026-01-01"}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := sources.Ledger.Balances(ctx, p, in, nil)
			if err != nil {
				errs <- err
				return
			}
			if len(result.Rows) != 1 || result.Rows[0].Amounts[0].Value.String() != "20000" {
				errs <- fmt.Errorf("unexpected balance: %v", result.Rows)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if n := reads.Load(); n != 1 {
		t.Fatalf("concurrent identical balances used %d reads", n)
	}
	grouped, err := sources.Ledger.Balances(ctx, p, ledger.BalancesInput{At: in.At, GroupBy: "commodity"}, nil)
	if err != nil || len(grouped.Rows) != 1 || grouped.Rows[0].Key != "EUR" || reads.Load() != 1 {
		t.Fatalf("grouped result: %v, %v, %d reads", grouped, err, reads.Load())
	}
	filtered, err := sources.Ledger.Balances(ctx, p, ledger.BalancesInput{At: in.At, Accounts: []string{"missing"}}, nil)
	if err != nil || len(filtered.Rows) != 0 || reads.Load() != 2 {
		t.Fatalf("filtered result: %v, %v, %d reads", filtered, err, reads.Load())
	}
	earlier, err := sources.Ledger.Balances(ctx, p, ledger.BalancesInput{At: "2025-12-31"}, nil)
	if err != nil || len(earlier.Rows) != 0 || reads.Load() != 3 {
		t.Fatalf("earlier result: %v, %v, %d reads", earlier, err, reads.Load())
	}
	other := p
	other.UserID = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	theirs, err := sources.Ledger.Balances(ctx, other, in, nil)
	if err != nil || len(theirs.Rows) != 1 || theirs.Rows[0].Amounts[0].Value.String() != "1" || reads.Load() != 4 {
		t.Fatalf("other user: %v, %v, %d reads", theirs, err, reads.Load())
	}
	grouped.Rows[0].Key = "changed"
	again, err := sources.Ledger.Balances(ctx, p, ledger.BalancesInput{At: in.At, GroupBy: "commodity"}, nil)
	if err != nil || again.Rows[0].Key != "EUR" || reads.Load() != 4 {
		t.Fatalf("result isolation: %v, %v, %d reads", again, err, reads.Load())
	}
}

func TestBalanceSeriesSharesCurrentBalance(t *testing.T) {
	ctx, p, sources, reads := postingsFixture(t)
	dates := []string{"2025-12-31", "2026-01-01"}
	wantBalance, err := sources.Ledger.Balances(ctx, p, ledger.BalancesInput{At: dates[1]}, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantSeries, err := sources.Ledger.BalanceSeries(ctx, p, dates, historyValuer{})
	if err != nil {
		t.Fatal(err)
	}
	shared := ledger.WithBalanceSeries(reqcache.With(ctx), dates)
	reads.Store(0)
	var gotBalance ledger.BalancesResult
	var gotSeries []ledger.BalancesResult
	var balanceErr, seriesErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		gotBalance, balanceErr = sources.Ledger.Balances(shared, p, ledger.BalancesInput{At: dates[1]}, nil)
	}()
	go func() {
		defer wg.Done()
		gotSeries, seriesErr = sources.Ledger.BalanceSeries(shared, p, dates, historyValuer{})
	}()
	wg.Wait()
	got, _ := json.Marshal([]any{gotBalance, gotSeries})
	want, _ := json.Marshal([]any{wantBalance, wantSeries})
	if balanceErr != nil || seriesErr != nil || string(got) != string(want) {
		t.Fatalf("shared balance differs: %v / %v", balanceErr, seriesErr)
	}
	if n := reads.Load(); n != 2 {
		t.Fatalf("shared series and balance used %d reads, expected one aggregate and one account read", n)
	}
	gotBalance.Rows[0].Amounts[0].Value = ledger.MustDecimal("999")
	again, err := sources.Ledger.Balances(shared, p, ledger.BalancesInput{At: dates[1]}, nil)
	if err != nil || again.Rows[0].Amounts[0].Value.String() != "20000" || reads.Load() != 2 {
		t.Fatal("shared results were mutable")
	}
	other := p
	other.UserID = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	theirs, err := sources.Ledger.Balances(shared, other, ledger.BalancesInput{At: dates[1]}, nil)
	if err != nil || len(theirs.Rows) != 1 || theirs.Rows[0].Amounts[0].Value.String() != "1" || reads.Load() != 3 {
		t.Fatal("shared balances crossed user boundaries")
	}
	filtered, err := sources.Ledger.Balances(shared, p, ledger.BalancesInput{At: dates[1], Accounts: []string{"missing"}}, nil)
	if err != nil || len(filtered.Rows) != 0 || reads.Load() != 4 {
		t.Fatal("filtered balance did not use its own read")
	}
	empty := p
	empty.UserID = "01ARZ3NDEKTSV4RRFFQ69G5FAX"
	emptyBalance, err := sources.Ledger.Balances(shared, empty, ledger.BalancesInput{At: dates[1]}, nil)
	if err != nil || len(emptyBalance.Rows) != 0 || reads.Load() != 5 {
		t.Fatal("empty balance did not reuse its series read")
	}
	emptySeries, err := sources.Ledger.BalanceSeries(shared, empty, dates, historyValuer{})
	if err != nil || len(emptySeries) != len(dates) || reads.Load() != 6 {
		t.Fatal("empty balance series did not reuse its journal read")
	}
	denied := p
	denied.Scopes = nil
	if _, err := sources.Ledger.Balances(shared, denied, ledger.BalancesInput{At: dates[1]}, nil); err == nil {
		t.Fatal("shared result bypassed authorization")
	}
}

func TestDashboardSharesAccountsAndBalanceHistory(t *testing.T) {
	ctx, p, _, _ := postingsFixture(t)
	parsed, _ := url.Parse(os.Getenv("MONEYBAG_TEST_MONGO_URI"))
	var journalReads atomic.Int64
	monitor := &event.CommandMonitor{Started: func(_ context.Context, e *event.CommandStartedEvent) {
		if e.CommandName == "aggregate" && e.Command.Lookup("aggregate").StringValue() == "journal" {
			journalReads.Add(1)
		}
	}}
	client, err := mongo.Connect(options.Client().ApplyURI(os.Getenv("MONEYBAG_TEST_MONGO_URI")).SetMonitor(monitor))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect(context.Background())
	db := client.Database(strings.TrimPrefix(parsed.Path, "/") + "_postings")
	book := ledger.NewService(ledger.NewMongoRepository(db), nil)
	expr, err := expressions.New()
	if err != nil {
		t.Fatal(err)
	}
	engine := dashboard.Engine{Expr: expr, Reader: &dashboard.Sources{Ledger: book, Valuer: historyValuer{}}}
	result, err := engine.Evaluate(ctx, p, "size(data.accounts) + size(data.history)", map[string]dashboard.Query{"accounts": {From: "accounts"}, "history": {From: "balance_history"}}, nil)
	if err != nil || result.Value == nil || journalReads.Load() != 1 {
		t.Fatalf("dashboard used %d journal aggregates: %v", journalReads.Load(), err)
	}
}

func TestPostedSourcesShareJournal(t *testing.T) {
	ctx, p, sources, reads := postingsFixture(t)
	digest := func(ctx context.Context, p principal.Principal, from, to string, effective, mutate bool) (string, int, error) {
		hash := sha256.New()
		encoder := json.NewEncoder(hash)
		n := 0
		err := sources.Ledger.PostedStream(ctx, p, from, to, effective, func(entry ledger.Entry) error {
			if err := encoder.Encode(entry); err != nil {
				return err
			}
			n++
			if mutate {
				if len(entry.Postings) > 0 {
					entry.Postings[0].Account = "modified"
				}
				if len(entry.Tags) > 0 {
					entry.Tags[0] = "modified"
				}
				if len(entry.Links) > 0 {
					entry.Links[0].ID = "modified"
				}
			}
			return nil
		})
		return fmt.Sprintf("%x", hash.Sum(nil)), n, err
	}
	full, fullCount, err := digest(ctx, p, "", "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	effective, effectiveCount, err := digest(ctx, p, "2026-01-01", "2026-01-01", true, false)
	if err != nil {
		t.Fatal(err)
	}
	shared := ledger.WithSharedPostedStream(reqcache.With(ctx))
	reads.Store(0)
	var wg sync.WaitGroup
	var a, b string
	var an, bn int
	var ae, be error
	wg.Add(2)
	go func() { defer wg.Done(); a, an, ae = digest(shared, p, "", "", false, true) }()
	go func() { defer wg.Done(); b, bn, be = digest(shared, p, "2026-01-01", "2026-01-01", true, false) }()
	wg.Wait()
	if ae != nil || be != nil || a != full || b != effective || an != fullCount || bn != effectiveCount {
		t.Fatalf("shared stream differs: %v / %v", ae, be)
	}
	if n := reads.Load(); n != 3 {
		t.Fatalf("shared streams used %d reads, expected journal, reversals and tags", n)
	}
	again, count, err := digest(shared, p, "", "", false, false)
	if err != nil || again != full || count != fullCount || reads.Load() != 3 {
		t.Fatal("callback mutation leaked into the shared stream")
	}
	_, count, err = digest(shared, p, "2026-01-02", "2026-01-31", false, false)
	if err != nil || count != 0 || reads.Load() != 3 {
		t.Fatal("date filter was not preserved")
	}
	other := p
	other.UserID = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	_, count, err = digest(shared, other, "", "", false, false)
	if err != nil || count != 1 {
		t.Fatal("shared stream crossed user boundaries")
	}
	denied := p
	denied.Scopes = nil
	if _, _, err = digest(shared, denied, "", "", false, false); err == nil {
		t.Fatal("shared stream bypassed authorization")
	}
	n := 0
	err = sources.Ledger.PostedStream(shared, p, "", "", false, func(ledger.Entry) error { n++; return fmt.Errorf("synthetic callback failure") })
	if err == nil || n != 1 {
		t.Fatal("callback error was not propagated")
	}
}

func TestDashboardSharesJournalAndPostings(t *testing.T) {
	ctx, p, sources, reads := postingsFixture(t)
	expr, err := expressions.New()
	if err != nil {
		t.Fatal(err)
	}
	engine := dashboard.Engine{Expr: expr, Reader: sources}
	data := map[string]dashboard.Query{"journal": {From: "journal", Agg: map[string]string{"n": "count()"}}, "postings": {From: "postings", Agg: map[string]string{"n": "count()"}}}
	reads.Store(0)
	result, err := engine.Evaluate(ctx, p, "size(data.journal) + size(data.postings)", data, nil)
	if err != nil || result.Value == nil || reads.Load() != 4 {
		t.Fatalf("dashboard used %d reads: %v", reads.Load(), err)
	}
}

func TestPostedStreamLargeCacheFallback(t *testing.T) {
	ctx, p, sources, reads := postingsFixture(t)
	parsed, _ := url.Parse(os.Getenv("MONEYBAG_TEST_MONGO_URI"))
	client, err := mongo.Connect(options.Client().ApplyURI(os.Getenv("MONEYBAG_TEST_MONGO_URI")))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect(context.Background())
	db := client.Database(strings.TrimPrefix(parsed.Path, "/") + "_postings")
	p.UserID = "01ARZ3NDEKTSV4RRFFQ69G5FAX"
	text := strings.Repeat("x", 1<<20)
	docs := make([]any, 33)
	for i := range docs {
		date := "2025-01-01"
		if i == 0 {
			date = "2026-01-01"
		}
		docs[i] = ledger.Entry{ID: fmt.Sprintf("large-%d", i), UserID: p.UserID, Date: date, Seq: int64(i + 1), Status: "posted", Kind: "entry", Payee: text}
	}
	if _, err = db.Collection("journal").InsertMany(ctx, docs); err != nil {
		t.Fatal(err)
	}
	shared := ledger.WithSharedPostedStream(reqcache.With(ctx))
	reads.Store(0)
	for i := 0; i < 2; i++ {
		n := 0
		err := sources.Ledger.PostedStream(shared, p, "2026-01-01", "2026-01-01", false, func(entry ledger.Entry) error {
			n++
			if entry.ID != "large-0" || len(entry.Payee) != len(text) {
				return fmt.Errorf("wrong fallback entry")
			}
			return nil
		})
		if err != nil || n != 1 {
			t.Fatalf("fallback %d failed: %v", i, err)
		}
	}
	if reads.Load() < 4 {
		t.Fatalf("large journal did not fall back to streaming: %d reads", reads.Load())
	}
}
