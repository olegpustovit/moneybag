package main

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres/pgtest"
	"github.com/olegpustovit/moneybag/backend/internal/mcpserver"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestDailyScheduleUsesUTCAndRollsOver(t *testing.T) {
	for _, tc := range []struct{ now, want string }{{"2026-10-28T02:59:59Z", "2026-10-28T03:00:00Z"}, {"2026-10-28T03:00:00Z", "2026-10-29T03:00:00Z"}, {"2026-12-31T04:00:00Z", "2027-01-01T03:00:00Z"}, {"2026-10-28T04:30:00+02:00", "2026-10-28T03:00:00Z"}} {
		now, _ := time.Parse(time.RFC3339, tc.now)
		if got := nextDaily(now, 3, time.UTC).Format(time.RFC3339); got != tc.want {
			t.Fatal(tc.now, got, tc.want)
		}
	}
}

func TestRateSyncScheduleFollowsZagrebClock(t *testing.T) {
	zg, e := time.LoadLocation("Europe/Zagreb")
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct{ now, want string }{{"2026-10-28T15:59:00Z", "2026-10-28T16:00:00Z"}, {"2026-10-28T16:00:00Z", "2026-10-29T16:00:00Z"}, {"2026-07-01T10:00:00Z", "2026-07-01T15:00:00Z"}} {
		now, _ := time.Parse(time.RFC3339, tc.now)
		if got := nextDaily(now, 17, zg).UTC().Format(time.RFC3339); got != tc.want {
			t.Fatal(tc.now, got, tc.want)
		}
	}
}

func TestMaintenanceStopsBeforeDatabaseDisconnect(t *testing.T) {
	stop := startJobs(context.Background(), mcpserver.Services{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("maintenance did not stop")
	}
}

func TestExpirySweepKeepsUnexpiredRows(t *testing.T) {
	pool := pgtest.New(t)
	ctx := context.Background()
	for _, table := range []string{"sessions", "invites", "oauth_codes", "admin_confirmations"} {
		query := `INSERT INTO ` + pgx.Identifier{table}.Sanitize() + `(id,expires_at) VALUES ('expired',now()-interval '1 hour'),('live',now()+interval '1 hour')`
		if _, err := pool.Exec(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO oauth_tokens(id,refresh_hash,access_expires_at,expires_at) VALUES ('expired','old',now()+interval '1 hour',now()-interval '1 hour'),('live','new',now()-interval '1 hour',now()+interval '1 hour')`); err != nil {
		t.Fatal(err)
	}
	if err := sweepExpired(ctx, pool); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"sessions", "invites", "oauth_codes", "oauth_tokens", "admin_confirmations"} {
		var ids []string
		if err := pool.QueryRow(ctx, `SELECT array_agg(id) FROM `+pgx.Identifier{table}.Sanitize()).Scan(&ids); err != nil || len(ids) != 1 || ids[0] != "live" {
			t.Fatalf("%s remaining: %v %v", table, ids, err)
		}
	}
}
