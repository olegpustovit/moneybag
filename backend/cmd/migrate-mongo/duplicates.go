package main

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type uniqueKey struct {
	columns []string
	unwind  string
}

var additionalKeys = map[string][][]string{
	"users": {{"email"}}, "accounts": {{"user_id", "id"}}, "journal": {{"user_id", "seq"}},
	"prices": {{"user_id", "pair", "at"}}, "oauth_tokens": {{"refresh_hash"}},
	"entry_tags": {{"user_id", "entry_id"}}, "budgets": {{"user_id", "id"}}, "rules": {{"user_id", "id"}},
	"scenarios": {{"user_id", "id"}}, "tables": {{"user_id", "id"}}, "rows": {{"user_id", "table_id", "id"}},
	"import_rows": {{"user_id", "account", "dedupe_key"}}, "dashboards": {{"user_id", "id"}},
	"dashboard_versions": {{"user_id", "dashboard_id", "version"}}, "views": {{"user_id", "id"}},
}

func sourceColumn(table mapping, column string) string {
	if column == "storage_key" || column == "id" && !slices.Contains(table.columns, "storage_key") {
		return "_id"
	}
	return column
}
func (m migration) checkDuplicates(ctx context.Context) error {
	for _, table := range mappings {
		keys := []uniqueKey{}
		if slices.Contains(table.columns, "storage_key") {
			keys = append(keys, uniqueKey{columns: []string{"storage_key"}})
		} else if slices.Contains(table.columns, "id") {
			keys = append(keys, uniqueKey{columns: []string{"id"}})
		}
		for _, columns := range additionalKeys[table.name] {
			keys = append(keys, uniqueKey{columns: columns})
		}
		if table.name == "rows" {
			keys = append(keys, uniqueKey{columns: []string{"user_id", "table_id", "key_hashes"}, unwind: "key_hashes"})
		}
		for _, key := range keys {
			group := bson.M{}
			for i, column := range key.columns {
				field := sourceColumn(table, column)
				var zero any = ""
				switch columnKinds[table.name][column] {
				case kindInteger, kindNumeric:
					zero = int64(0)
				case kindBool:
					zero = false
				}
				var expression any = bson.M{"$ifNull": bson.A{"$" + field, zero}}
				if field == "_id" {
					expression = bson.M{"$toString": "$_id"}
				}
				if table.name == "prices" && column == "at" {
					expression = bson.M{"$toDate": "$at"}
				}
				group[fmt.Sprintf("k%d", i)] = expression
			}
			pipeline := mongo.Pipeline{}
			if key.unwind != "" {
				pipeline = append(pipeline, bson.D{{Key: "$unwind", Value: "$" + key.unwind}})
			}
			pipeline = append(pipeline, bson.D{{Key: "$group", Value: bson.M{"_id": group, "count": bson.M{"$sum": 1}}}}, bson.D{{Key: "$match", Value: bson.M{"count": bson.M{"$gt": 1}}}}, bson.D{{Key: "$limit", Value: 1}})
			cursor, err := m.source.Collection(table.name).Aggregate(ctx, pipeline)
			if err != nil {
				return err
			}
			duplicate := cursor.Next(ctx)
			err = cursor.Err()
			cursor.Close(ctx)
			if err != nil {
				return err
			}
			if duplicate {
				return fmt.Errorf("source duplicates violate %s unique key (%s); resolve them explicitly before copying", table.name, strings.Join(key.columns, ","))
			}
		}
	}
	return nil
}
