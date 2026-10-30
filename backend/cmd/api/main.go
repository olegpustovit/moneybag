package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/gofiber/fiber/v3"
	"github.com/olegpustovit/moneybag/backend/cmd/api/handlers"
	"github.com/olegpustovit/moneybag/backend/internal/components/admin"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/budget"
	"github.com/olegpustovit/moneybag/backend/internal/components/classify"
	"github.com/olegpustovit/moneybag/backend/internal/components/consistency"
	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/components/feedback"
	"github.com/olegpustovit/moneybag/backend/internal/components/history"
	"github.com/olegpustovit/moneybag/backend/internal/components/identity"
	"github.com/olegpustovit/moneybag/backend/internal/components/imports"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/model"
	"github.com/olegpustovit/moneybag/backend/internal/components/oauth"
	"github.com/olegpustovit/moneybag/backend/internal/components/preview"
	"github.com/olegpustovit/moneybag/backend/internal/components/rates"
	"github.com/olegpustovit/moneybag/backend/internal/components/reconcile"
	"github.com/olegpustovit/moneybag/backend/internal/components/semantic"
	"github.com/olegpustovit/moneybag/backend/internal/components/sharing"
	"github.com/olegpustovit/moneybag/backend/internal/components/tables"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/mongodb"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/screenshot"
	"github.com/olegpustovit/moneybag/backend/internal/mcpserver"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // Europe/Zagreb must resolve in minimal containers
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("Moneybag stopped", "error", err)
		os.Exit(1)
	}
}

// loopbackURL is how Chromium reaches this process directly, skipping the proxy and the public name.
func loopbackURL(listen string) string {
	host, port, _ := net.SplitHostPort(listen)
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}
func run() error {
	cfg, e := loadConfig()
	if e != nil {
		return e
	}
	if e := handlers.ValidateSPA(); e != nil {
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
	if e = ids.EachUser(ctx, func(p principal.Principal) error { return bookRepo.Init(ctx, p) }); e != nil {
		return fmt.Errorf("initialize ledger indexes: %w", e)
	}
	book := ledger.NewService(bookRepo, a)
	expr, e := expressions.New()
	if e != nil {
		return e
	}
	book.Expr = expr
	prices := rates.NewService(rates.NewMongoRepository(db), a)
	budgets := budget.NewService(budget.NewMongoRepository(db), a, prices)
	tab := tables.NewService(tables.NewMongoRepository(db), a, expr)
	models := model.NewService(model.NewMongoRepository(db), tab, a, expr)
	models.Ledger, models.Rates, models.Budget = book, prices, budgets
	sources := &dashboard.Sources{Ledger: book, Budget: budgets, Model: models, Tables: tab, Valuer: prices, Rates: prices}
	models.Views = func(ctx context.Context, p principal.Principal, id string) ([]map[string]any, error) {
		return sources.Rows(ctx, p, "view:"+id)
	}
	meaning := semantic.NewService(semantic.NewMongoRepository(db), book, prices, a, expr)
	dash := dashboard.NewService(dashboard.NewMongoRepository(db), &dashboard.Engine{Expr: expr, Reader: sources, Metrics: meaning}, a)
	sources.Views, sources.Expr = dash, expr
	classes := classify.NewService(classify.NewMongoRepository(db), book, a, expr)
	rec := reconcile.NewService(reconcile.NewMongoRepository(db), book, a)
	imp := imports.NewService(imports.NewMongoRepository(db), book, classes, rec, a)
	imp.SetSecretKey(cfg.SecretKey)
	shares := sharing.NewService(sharing.NewMongoRepository(db), ids, dash, sources, expr, a)
	services := mcpserver.Services{Identity: ids, Ledger: book, Audit: a, Rates: prices, Budget: budgets, Tables: tab, Model: models, Dashboard: dash, Expr: expr, Sources: sources, Logger: log}
	services.Classify = classes
	services.Semantic, services.PublicURL = meaning, cfg.PublicURL
	services.Feedback = feedback.NewService(feedback.NewMongoRepository(db), a)
	services.Admin = admin.NewService(admin.NewMongoRepository(db), ids, auth, a, cfg.PublicURL)
	if e = services.Admin.Init(ctx); e != nil {
		return fmt.Errorf("initialize admin: %w", e)
	}
	services.Consistency = &consistency.Service{Ledger: book, Reconcile: rec, Dashboard: dash, Model: models, Tables: tab, Budget: budgets, Rates: prices}
	dash.Lint = func(ctx context.Context, p principal.Principal, spec dashboard.Spec) ([]string, error) {
		return services.Consistency.SpecLint(ctx, p, spec)
	}
	services.Imports = imp
	services.Reconcile = rec
	services.Sharing = shares
	var capture preview.Capture
	if cfg.Chromium != "" {
		capture = screenshot.New(cfg.Chromium).Capture
	}
	services.Previews = preview.New(loopbackURL(cfg.Listen), capture)
	dash.RenderCheck = func(ctx context.Context, p principal.Principal, id string, version int) error {
		failures, e := services.Previews.Gate(ctx, p, "", preview.Target{Dashboard: id, Version: strconv.Itoa(version)})
		if e != nil {
			return e
		}
		if len(failures) > 0 {
			return errors.New(strings.Join(failures, "; "))
		}
		return nil
	}
	services.History = &history.Service{Reconcile: rec, Audit: a, Ledger: book, Budget: budgets, Classify: classes, Dashboard: dash, Imports: imp, Model: models, Rates: prices, Sharing: shares, Tables: tab, Semantic: meaning, Feedback: services.Feedback, Admin: services.Admin}
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
	snapshots, e := dashboard.NewSnapshots(a)
	if e != nil {
		return e
	}
	sources.Snapshots = snapshots
	dash.UseSnapshots(snapshots)
	onChange, stopPrewarm := prewarmer(ctx, snapshots, dash, log)
	a.OnChange = onChange
	defer stopPrewarm()
	stopJobs := startJobs(ctx, services, log)
	defer stopJobs()
	web := handlers.New(handlers.Config{PublicURL: cfg.PublicURL, TailnetURL: cfg.TailnetURL, SetupToken: cfg.SetupToken, Version: version, Toolsets: cfg.Toolsets}, services, auth, bookRepo, client)
	app := fiber.New(fiber.Config{BodyLimit: 12 * 1024 * 1024, ReadTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second})
	compress, e := handlers.Compression()
	if e != nil {
		return fmt.Errorf("configure HTTP compression: %w", e)
	}
	app.All("/mcp", compress(web.RemoteMCP(false)))
	app.All("/mcp/readonly", compress(web.RemoteMCP(true)))
	app.All("/*", compress(web.Handler()))
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
