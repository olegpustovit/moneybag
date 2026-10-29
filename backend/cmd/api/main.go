package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/gofiber/fiber/v3"
	"github.com/olegpustovit/moneybag/backend/cmd/api/handlers"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/budget"
	"github.com/olegpustovit/moneybag/backend/internal/components/classify"
	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/components/history"
	"github.com/olegpustovit/moneybag/backend/internal/components/identity"
	"github.com/olegpustovit/moneybag/backend/internal/components/imports"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/model"
	"github.com/olegpustovit/moneybag/backend/internal/components/oauth"
	"github.com/olegpustovit/moneybag/backend/internal/components/preview"
	"github.com/olegpustovit/moneybag/backend/internal/components/rates"
	"github.com/olegpustovit/moneybag/backend/internal/components/reconcile"
	"github.com/olegpustovit/moneybag/backend/internal/components/sharing"
	"github.com/olegpustovit/moneybag/backend/internal/components/tables"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/mongodb"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/screenshot"
	"github.com/olegpustovit/moneybag/backend/internal/mcpserver"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("Moneybag stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	cfg, e := loadConfig()
	if e != nil {
		return e
	}
	var handler slog.Handler = slog.NewTextHandler(os.Stdout, nil)
	if cfg.LogFormat == "json" {
		handler = slog.NewJSONHandler(os.Stdout, nil)
	}
	log := slog.New(handler)
	slog.SetDefault(log)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer cancel()
	connectCtx, stop := context.WithTimeout(ctx, 10*time.Second)
	client, e := mongodb.Connect(connectCtx, cfg.MongoURI)
	stop()
	if e != nil {
		return e
	}
	defer client.Disconnect(context.Background())
	db := client.Database(cfg.MongoDB)
	ids := identity.NewService(identity.NewMongoRepository(db))
	if e = ids.Init(ctx); e != nil {
		return fmt.Errorf("initialize identity: %w", e)
	}
	auth := oauth.NewService(oauth.NewMongoRepository(db), ids, cfg.PublicURL)
	if e = auth.Init(ctx); e != nil {
		return fmt.Errorf("initialize OAuth: %w", e)
	}
	a := audit.NewService(audit.NewMongoRepository(db))
	bookRepo := ledger.NewMongoRepository(db)
	book := ledger.NewService(bookRepo, a)
	expr, e := expressions.New()
	if e != nil {
		return e
	}
	prices := rates.NewService(rates.NewMongoRepository(db), a)
	budgets := budget.NewService(budget.NewMongoRepository(db), a, prices)
	tab := tables.NewService(tables.NewMongoRepository(db), a, expr)
	models := model.NewService(model.NewMongoRepository(db), tab, a, expr)
	models.Ledger, models.Rates, models.Budget = book, prices, budgets
	sources := &dashboard.Sources{Ledger: book, Budget: budgets, Model: models, Tables: tab, Valuer: prices, Rates: prices}
	dash := dashboard.NewService(dashboard.NewMongoRepository(db), &dashboard.Engine{Expr: expr, Reader: sources}, a)
	classes := classify.NewService(classify.NewMongoRepository(db), book, a, expr)
	rec := reconcile.NewService(reconcile.NewMongoRepository(db), book, a)
	imp := imports.NewService(imports.NewMongoRepository(db), book, classes, rec, a)
	imp.SetSecretKey(cfg.SecretKey)
	shares := sharing.NewService(sharing.NewMongoRepository(db), ids, dash, sources, expr, a)
	services := mcpserver.Services{Identity: ids, Ledger: book, Audit: a, Rates: prices, Budget: budgets, Tables: tab, Model: models, Dashboard: dash, Expr: expr, Sources: sources, Logger: log}
	services.Classify = classes
	services.Imports = imp
	services.Reconcile = rec
	services.Sharing = shares
	services.Previews = preview.New(cfg.PublicURL, handlers.Stylesheet(), screenshot.New(cfg.Chromium), shares)
	services.History = &history.Service{Reconcile: rec, Audit: a, Ledger: book, Budget: budgets, Classify: classes, Dashboard: dash, Imports: imp, Model: models, Rates: prices, Sharing: shares, Tables: tab}
	if len(os.Args) > 1 && os.Args[1] == "bootstrap" {
		u, e := ids.Setup(ctx, os.Getenv("MONEYBAG_OWNER_EMAIL"), os.Getenv("MONEYBAG_OWNER_NAME"), os.Getenv("MONEYBAG_OWNER_PASSWORD"))
		if e != nil {
			return e
		}
		if e = bookRepo.Init(ctx, identity.Principal(u)); e != nil {
			return e
		}
		return models.Initialize(ctx, identity.Principal(u))
	}
	if len(os.Args) > 1 && os.Args[1] == "verify" {
		return ids.EachUser(ctx, func(p principal.Principal) error { return book.Verify(ctx, p) })
	}
	stopJobs := startJobs(ctx, services, log)
	defer stopJobs()
	web := handlers.New(handlers.Config{PublicURL: cfg.PublicURL, SetupToken: cfg.SetupToken, Version: version, Toolsets: cfg.Toolsets}, services, auth, bookRepo, client)
	app := fiber.New(fiber.Config{BodyLimit: 12 * 1024 * 1024, ReadTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second})
	app.All("/mcp", web.RemoteMCP(false))
	app.All("/mcp/readonly", web.RemoteMCP(true))
	app.All("/*", web.Handler())
	done := make(chan error, 1)
	go func() { done <- app.Listen(cfg.Listen, fiber.ListenConfig{DisableStartupMessage: true}) }()
	log.Info("Moneybag listening", "address", cfg.Listen, "version", version)
	select {
	case e := <-done:
		if e != nil {
			return fmt.Errorf("serve: %w", e)
		}
	case <-ctx.Done():
		shutdownCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		if e = app.ShutdownWithContext(shutdownCtx); e != nil && !errors.Is(e, context.Canceled) {
			return fmt.Errorf("shutdown: %w", e)
		}
	}
	return nil
}
