package main

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/oklog/ulid/v2"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres/pgtest"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestCopyVerifyAndCorruption(t *testing.T) {
	uri := os.Getenv("MONEYBAG_MIGRATE_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("set MONEYBAG_MIGRATE_TEST_MONGO_URI to a dedicated disposable source server")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect(context.Background())
	db := client.Database("moneybag_migrate_test_" + ulid.Make().String())
	defer db.Drop(context.Background())
	pool := pgtest.New(t)
	m := migration{source: db, target: pool, out: io.Discard}
	decimal, err := bson.ParseDecimal128("1.50")
	if err != nil {
		t.Fatal(err)
	}
	negative, err := bson.ParseDecimal128("-1.50")
	if err != nil {
		t.Fatal(err)
	}
	fixtures := map[string][]any{
		"accounts":            {bson.M{"_id": "owner:assets:bank", "user_id": "owner", "id": "assets:bank", "type": "asset", "commodities": bson.A{"EUR"}}},
		"journal":             {bson.M{"_id": "entry", "user_id": "owner", "seq": int64(1), "date": "2026-01-01", "status": "posted", "payee": "Original", "postings": bson.A{bson.M{"account": "assets:bank", "amount": bson.M{"v": decimal, "c": "EUR"}}, bson.M{"account": "expenses:food", "amount": bson.M{"v": negative, "c": "EUR"}}}}},
		"counters":            {bson.M{"_id": "owner:journal", "user_id": "owner", "name": "journal", "n": int64(1)}},
		"credentials":         {bson.M{"_id": "owner", "hash": []byte{0, 1, 2, 255}, "salt": []byte{0, 3, 4, 255}}},
		"rows":                {bson.M{"_id": "owner:table:row", "user_id": "owner", "table_id": "table", "id": "row", "version": 1, "cells": bson.M{"computed": decimal, "plain": "1.50"}, "key_hashes": bson.A{"synthetic-key"}}},
		"dashboard_snapshots": {bson.M{"_id": "frozen", "owner_user": "owner", "dashboard_id": "dashboard", "rendered": bson.M{"html": "<p>Frozen</p>"}}},
	}
	for name, docs := range fixtures {
		if _, err = db.Collection(name).InsertMany(ctx, docs); err != nil {
			t.Fatal(err)
		}
	}
	if err = m.copy(ctx, false); err != nil {
		t.Fatal(err)
	}
	if err = m.verify(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE row_keys SET key_hash='wrong-key' WHERE row_id='row'`); err != nil {
		t.Fatal(err)
	}
	if err = m.verify(ctx); err == nil || !strings.Contains(err.Error(), "row_keys mapping") {
		t.Fatalf("corrupted unique-key mapping verified: %v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE row_keys SET key_hash='synthetic-key' WHERE row_id='row'`); err != nil {
		t.Fatal(err)
	}
	if err = m.copy(ctx, false); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("nonempty target accepted: %v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE journal SET payee='Corrupted' WHERE id='entry'`); err != nil {
		t.Fatal(err)
	}
	if err = m.verify(ctx); err == nil {
		t.Fatal("corrupted document verified")
	}
	if err = m.copy(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err = m.verify(ctx); err != nil {
		t.Fatal(err)
	}
	var native []string
	if err = pool.QueryRow(ctx, `SELECT decimal_cells FROM rows WHERE id='row'`).Scan(&native); err != nil || len(native) != 1 || native[0] != "computed" {
		t.Fatalf("decimal formula metadata: %v %v", native, err)
	}
	var html string
	if err = pool.QueryRow(ctx, `SELECT rendered->>'html' FROM dashboard_snapshots WHERE id='frozen'`).Scan(&html); err != nil || html != "<p>Frozen</p>" {
		t.Fatalf("legacy HTML: %s %v", html, err)
	}
	var hash []byte
	if err = pool.QueryRow(ctx, `SELECT hash FROM credentials WHERE id='owner'`).Scan(&hash); err != nil || string(hash) != string([]byte{0, 1, 2, 255}) {
		t.Fatal("credential bytes changed", err)
	}
	if _, err = db.Collection("unknown").InsertOne(ctx, bson.M{"_id": "unknown"}); err != nil {
		t.Fatal(err)
	}
	if err = m.copy(ctx, true); err == nil || !strings.Contains(err.Error(), "unmapped source collection") {
		t.Fatalf("unknown collection accepted: %v", err)
	}
	if err = db.Collection("unknown").Drop(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Collection("accounts").InsertOne(ctx, bson.M{"_id": "another-storage-key", "user_id": "owner", "id": "assets:bank"}); err != nil {
		t.Fatal(err)
	}
	if err = m.copy(ctx, true); err == nil || !strings.Contains(err.Error(), "duplicates") {
		t.Fatalf("duplicate logical account accepted: %v", err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM accounts WHERE user_id=@user`, pgx.NamedArgs{"user": "owner"}).Scan(&count); err != nil || count != 1 {
		t.Fatal("failed preflight changed target", err)
	}
}

func TestMappingIncludesEveryColumnKind(t *testing.T) {
	seen := map[string]bool{}
	for _, table := range mappings {
		if seen[table.name] {
			t.Fatal("duplicate mapping", table.name)
		}
		seen[table.name] = true
		for _, column := range table.columns {
			if _, ok := columnKinds[table.name][column]; !ok {
				t.Fatalf("missing kind for %s.%s", table.name, column)
			}
		}
	}
	if len(mappings) != 44 {
		t.Fatal("incomplete collection map", len(mappings))
	}
}

func TestProfilerRequiresExplicitPrivateArchive(t *testing.T) {
	uri := os.Getenv("MONEYBAG_MIGRATE_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("set MONEYBAG_MIGRATE_TEST_MONGO_URI to a dedicated disposable source server")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect(context.Background())
	db := client.Database("moneybag_migrate_test_" + ulid.Make().String())
	defer db.Drop(context.Background())
	if err = db.RunCommand(ctx, bson.D{{Key: "profile", Value: 2}}).Err(); err != nil {
		t.Fatal(err)
	}
	m := migration{source: db, target: pgtest.New(t), out: io.Discard}
	if err = m.copy(ctx, false); err == nil || !strings.Contains(err.Error(), "--profile-archive") {
		t.Fatalf("profiler silently skipped: %v", err)
	}
	m.profileArchive = t.TempDir() + "/profile.bson"
	if err = m.copy(ctx, false); err != nil {
		t.Fatal(err)
	}
	if err = m.verify(ctx); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(m.profileArchive)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("profile archive is not private", err)
	}
	if err = m.archiveProfile(ctx); err == nil {
		t.Fatal("existing archive was overwritten")
	}
}
