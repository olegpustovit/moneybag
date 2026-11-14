package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres/pgtest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/reconcile"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

func TestReconcileStatusAggregation(t *testing.T) {
	ctx := context.Background()
	var reads atomic.Int64
	pool := tracedPool(t, sqlQueryCounter{reads: &reads})
	var err error
	p := principal.Principal{UserID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Scopes: []string{"ledger:read"}}
	docs := []reconcile.Assertion{
		{ID: "a1", UserID: p.UserID, Account: "a", Date: "2026-01-01", OK: true},
		{ID: "a2", UserID: p.UserID, Account: "a", Date: "2026-02-01", OK: false},
		{ID: "b1", UserID: p.UserID, Account: "b", Date: "2026-03-01", OK: false},
		{ID: "b2", UserID: p.UserID, Account: "b", Date: "2026-04-01", OK: true},
		{ID: "c1", UserID: "other", Account: "c", Date: "2026-05-01", OK: true},
	}
	if _, err = insertAssertions(ctx, pool, docs...); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE balance_assertions SET archived=true WHERE id='b2'`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO discrepancies(id,user_id,assertion_id,account,status,diff) VALUES ('d',@user,'x','c','open','{"v":"0","c":"EUR"}')`, pgx.NamedArgs{"user": p.UserID}); err != nil {
		t.Fatal(err)
	}
	s := reconcile.NewService(reconcile.NewPostgresRepository(pool), nil, nil)
	reads.Store(0)
	got, err := s.Status(ctx, p, "")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"account":"a","last_ok":"2026-01-01","open_discrepancies":[]},{"account":"b","last_ok":"","open_discrepancies":[]},{"account":"c","last_ok":"","open_discrepancies":[{"id":"d","assertion_id":"x","account":"c","diff":{"v":"0","c":"EUR"},"status":"open"}]}]`
	if string(encoded) != want {
		t.Fatalf("status: %s", encoded)
	}
	if reads.Load() != 2 {
		t.Fatalf("read operations: %d", reads.Load())
	}
	got, err = s.Status(ctx, p, "a")
	if err != nil || len(got) != 1 || got[0].LastOK != "2026-01-01" {
		t.Fatalf("account filter: %v, %v", got, err)
	}
	many := make([]reconcile.Assertion, 150)
	for i := range many {
		many[i] = reconcile.Assertion{ID: fmt.Sprintf("extra-%03d", i), UserID: p.UserID, Account: fmt.Sprintf("extra-%03d", i), Date: "2026-01-01", OK: true}
	}
	if _, err := insertAssertions(ctx, pool, many...); err != nil {
		t.Fatal(err)
	}
	reads.Store(0)
	got, err = s.Status(ctx, p, "")
	if err != nil || len(got) != 153 || reads.Load() != 2 {
		t.Fatalf("many accounts: %d accounts, %d reads, %v", len(got), reads.Load(), err)
	}
}

func TestReconcileStatusBatchedVerification(t *testing.T) {
	ctx := context.Background()
	var reads atomic.Int64
	pool := tracedPool(t, sqlQueryCounter{reads: &reads})
	var err error
	p := principal.Principal{UserID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Scopes: []string{"ledger:read", "ledger:write"}}
	assertions := make([]reconcile.Assertion, 151)
	for i := range assertions {
		account := fmt.Sprintf("account-%03d", i)
		amount := ledger.MustDecimal("0")
		if i == 0 {
			amount = ledger.MustDecimal("3.5")
		}
		date := "2026-02-01"
		if i == 1 {
			date = ""
		}
		assertions[i] = reconcile.Assertion{ID: account, UserID: p.UserID, Account: account, Date: date, Amount: ledger.Amount{Value: amount, Commodity: "EUR"}, Source: "test", OK: true, Diff: ledger.MustDecimal("0")}
	}
	if _, err := insertAssertions(ctx, pool, assertions...); err != nil {
		t.Fatal(err)
	}
	posting := func(account, amount, currency string) ledger.Posting {
		return ledger.Posting{Account: account, Amount: ledger.Amount{Value: ledger.MustDecimal(amount), Commodity: currency}}
	}
	journal := []ledger.Entry{
		{ID: "j1", UserID: p.UserID, Seq: 1, Date: "2026-01-01", Status: "posted", Postings: []ledger.Posting{posting("account-000", "3.5", "EUR"), posting("account-000", "8", "USD"), posting("other", "99", "EUR")}},
		{ID: "j2", UserID: p.UserID, Seq: 2, Date: "2026-03-01", Status: "posted", Postings: []ledger.Posting{posting("account-000", "9", "EUR")}},
		{ID: "j3", UserID: p.UserID, Seq: 3, Date: "2026-01-01", Status: "draft", Postings: []ledger.Posting{posting("account-000", "9", "EUR")}},
		{ID: "j4", UserID: "other", Seq: 1, Date: "2026-01-01", Status: "posted", Postings: []ledger.Posting{posting("account-000", "9", "EUR")}},
	}
	if _, err = insertJournal(ctx, pool, journal...); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO discrepancies(id,user_id,assertion_id,account,diff,status) VALUES ('orphan',@user,'missing','orphan','{"v":"1","c":"EUR"}','open')`, pgx.NamedArgs{"user": p.UserID}); err != nil {
		t.Fatal(err)
	}
	s := reconcile.NewService(reconcile.NewPostgresRepository(pool), ledger.NewService(ledger.NewPostgresRepository(pgtest.New(t)), nil), nil)
	reads.Store(0)
	got, err := s.Status(ctx, p, "")
	if err != nil || len(got) != 152 || reads.Load() != 2 {
		t.Fatalf("status: %d accounts, %d reads, %v", len(got), reads.Load(), err)
	}
	if got[0].LastOK != "2026-02-01" || len(got[0].Discrepancies) != 0 {
		t.Fatalf("unexpected balance status: %+v", got[0])
	}
	readOnly := p
	readOnly.Scopes = []string{"ledger:read"}
	readResult, err := s.Status(ctx, readOnly, "")
	if err != nil {
		t.Fatal(err)
	}
	writeJSON, _ := json.Marshal(got)
	readJSON, _ := json.Marshal(readResult)
	if string(writeJSON) != string(readJSON) {
		t.Fatal("read-only and writable status differ")
	}
	reads.Store(0)
	filtered, err := s.Status(ctx, p, "account-000")
	if err != nil || len(filtered) != 1 || filtered[0].LastOK != "2026-02-01" || reads.Load() != 2 {
		t.Fatalf("filtered status: %+v, %d reads, %v", filtered, reads.Load(), err)
	}

	if _, err = insertAccount(ctx, pool, ledger.Account{Key: p.UserID + ":bank", UserID: p.UserID, ID: "bank", Commodities: []string{"EUR"}}); err != nil {
		t.Fatal(err)
	}
	s = reconcile.NewService(reconcile.NewPostgresRepository(pool), ledger.NewService(ledger.NewPostgresRepository(pgtest.New(t)), nil), audit.NewService(audit.NewPostgresRepository(pgtest.New(t))))
	for _, date := range []string{"2026-01-01", "2026-02-01"} {
		if _, err = s.Assert(ctx, p, reconcile.AssertInput{Account: "bank", Date: date, Amount: ledger.Amount{Value: ledger.MustDecimal("0"), Commodity: "EUR"}, Source: "test"}); err != nil {
			t.Fatal(err)
		}
	}
	entry := ledger.Entry{ID: "backdated", Seq: 4, UserID: p.UserID, Date: "2026-01-15", Status: "posted", Postings: []ledger.Posting{posting("bank", "3.5", "EUR")}}
	if _, err = insertJournal(ctx, pool, entry); err != nil {
		t.Fatal(err)
	}
	changed, err := s.Recheck(ctx, p, "bank")
	if err != nil || len(changed) != 1 || changed[0].LastOK != "2026-01-01" || len(changed[0].Discrepancies) != 1 || changed[0].Discrepancies[0].Diff.Value.String() != "-3.5" {
		t.Fatalf("changed status: %+v, %v", changed, err)
	}
	if _, err = pool.Exec(ctx, `DELETE FROM journal WHERE id='backdated'`); err != nil {
		t.Fatal(err)
	}
	changed, err = s.Recheck(ctx, p, "bank")
	if err != nil || len(changed) != 1 || changed[0].LastOK != "2026-02-01" || len(changed[0].Discrepancies) != 0 {
		t.Fatalf("restored status: %+v, %v", changed, err)
	}
	extra := make([]reconcile.Assertion, 4850)
	for i := range extra {
		account := fmt.Sprintf("extra-%04d", i)
		extra[i] = reconcile.Assertion{ID: account, UserID: p.UserID, Account: account, Date: "2026-02-01", Amount: ledger.Amount{Value: ledger.MustDecimal("0"), Commodity: "EUR"}, Source: "test", OK: true, Diff: ledger.MustDecimal("0")}
	}
	if _, err = insertAssertions(ctx, pool, extra...); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Status(ctx, p, ""); err == nil || !strings.Contains(err.Error(), "too many assertions") {
		t.Fatalf("assertion limit: %v", err)
	}
}
