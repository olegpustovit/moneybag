package main

import (
	"context"
	"github.com/olegpustovit/moneybag/backend/internal/mcpserver"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestDailyScheduleUsesUTCAndRollsOver(t *testing.T) {
	for _, tc := range []struct{ now, want string }{{"2026-10-28T02:59:59Z", "2026-10-28T03:00:00Z"}, {"2026-10-28T03:00:00Z", "2026-10-29T03:00:00Z"}, {"2026-12-31T04:00:00Z", "2027-01-01T03:00:00Z"}, {"2026-10-28T04:30:00+02:00", "2026-10-28T03:00:00Z"}} {
		now, _ := time.Parse(time.RFC3339, tc.now)
		if got := nextDaily(now, 3).Format(time.RFC3339); got != tc.want {
			t.Fatal(tc.now, got, tc.want)
		}
	}
}

func TestMaintenanceStopsBeforeDatabaseDisconnect(t *testing.T) {
	stop := startJobs(context.Background(), mcpserver.Services{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("maintenance did not stop")
	}
}
