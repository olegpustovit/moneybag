package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

func TestStorageErrorsAreRedactedAndCancellationStaysInterrupted(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"sql", &pgconn.PgError{Code: "23514", Message: "sensitive-storage-detail"}, "storage operation failed; inspect audit history before retrying a write"},
		{"network", &net.OpError{Op: "read", Net: "tcp", Err: errors.New("sensitive-storage-detail")}, "storage operation failed; inspect audit history before retrying a write"},
		{"cancelled", context.Canceled, "operation interrupted; inspect history before retrying a write"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := principal.With(context.Background(), principal.Principal{UserID: testUser, Scopes: []string{"ledger:read"}})
			server := mcp.NewServer(&mcp.Implementation{Name: "storage-test"}, nil)
			r := registration{server: server, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), tools: map[string]toolInfo{}}
			add(r, "storage_test", "ledger", "ledger:read", "test", true, false, func(context.Context, principal.Principal, Empty) (Empty, error) { return Empty{}, tc.err })
			ct, st := mcp.NewInMemoryTransports()
			ss, err := server.Connect(ctx, st, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer ss.Close()
			cs, err := mcp.NewClient(&mcp.Implementation{Name: "storage-client"}, nil).Connect(ctx, ct, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer cs.Close()
			result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "storage_test", Arguments: map[string]any{}})
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(result.Content)
			if err != nil || !result.IsError || !strings.Contains(string(encoded), tc.want) || strings.Contains(string(encoded), "sensitive-storage-detail") {
				t.Fatalf("public error: %s %v", encoded, err)
			}
		})
	}
}
