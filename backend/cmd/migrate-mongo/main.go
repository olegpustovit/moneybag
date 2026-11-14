package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type migration struct {
	source         *mongo.Database
	target         *pgxpool.Pool
	out            io.Writer
	profileArchive string
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "migration:", err)
		os.Exit(1)
	}
}
func run(args []string, out io.Writer) error {
	if len(args) == 0 || (args[0] != "copy" && args[0] != "verify") {
		return errors.New("use copy or verify with --mongo, --mongo-db and --pg")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	mongoURL := flags.String("mongo", "", "source URI; otherwise MONEYBAG_MIGRATE_MONGO_URI")
	database := flags.String("mongo-db", "moneybag", "source database")
	pgURL := flags.String("pg", "", "target URL; otherwise MONEYBAG_DATABASE_URL")
	profileArchive := flags.String("profile-archive", "", "explicit raw BSON archive for mutable system.profile, if present")
	truncate := flags.Bool("truncate", false, "explicitly replace target application data before copying")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if *mongoURL == "" {
		*mongoURL = os.Getenv("MONEYBAG_MIGRATE_MONGO_URI")
	}
	if *pgURL == "" {
		*pgURL = os.Getenv("MONEYBAG_DATABASE_URL")
	}
	if *mongoURL == "" || *pgURL == "" || *database == "" {
		return errors.New("source URI, database and target URL are required")
	}
	if *truncate && args[0] != "copy" {
		return errors.New("--truncate is only valid for copy")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	client, err := mongo.Connect(options.Client().ApplyURI(*mongoURL))
	if err != nil {
		return errors.New("invalid MongoDB source connection")
	}
	defer client.Disconnect(context.Background())
	if err = client.Ping(ctx, nil); err != nil {
		return errors.New("cannot connect to MongoDB source")
	}
	pool, err := postgres.Open(ctx, *pgURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err = postgres.Migrate(ctx, pool); err != nil {
		return err
	}
	m := migration{source: client.Database(*database), target: pool, out: out, profileArchive: *profileArchive}
	if args[0] == "copy" {
		return m.copy(ctx, *truncate)
	}
	return m.verify(ctx)
}
