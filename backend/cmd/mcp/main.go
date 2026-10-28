package main

import (
	"context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/olegpustovit/moneybag/backend/internal/mcpserver"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	endpoint := os.Getenv("MONEYBAG_MCP_URL")
	if endpoint == "" {
		endpoint = "https://moneybag.microapps.space/mcp"
	}
	server, remote, e := mcpserver.Proxy(ctx, endpoint, os.Getenv("MONEYBAG_MCP_TOKEN_FILE"), log)
	if e == nil {
		defer remote.Close()
		e = server.Run(ctx, &mcp.StdioTransport{})
	}
	if e != nil && ctx.Err() == nil {
		log.Error("MCP adapter stopped", "error", e)
		os.Exit(1)
	}
}
