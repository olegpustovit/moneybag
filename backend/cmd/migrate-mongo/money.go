package main

import (
	"context"
	"fmt"
	"math/big"
	"strconv"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type moneyKey struct{ user, account, currency string }

func (m migration) verifyMoney(ctx context.Context) error {
	cursor, err := m.source.Collection("journal").Aggregate(ctx, mongo.Pipeline{
		bson.D{{Key: "$unwind", Value: "$postings"}},
		bson.D{{Key: "$group", Value: bson.M{"_id": bson.M{"user": "$user_id", "account": "$postings.account", "currency": "$postings.amount.c"}, "total": bson.M{"$sum": "$postings.amount.v"}}}},
	})
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)
	expected := map[moneyKey]*big.Rat{}
	for cursor.Next(ctx) {
		var row struct {
			Key struct {
				User     string `bson:"user"`
				Account  string `bson:"account"`
				Currency string `bson:"currency"`
			} `bson:"_id"`
			Total bson.RawValue `bson:"total"`
		}
		if err = decodeLegacy(cursor.Current, &row); err != nil {
			return err
		}
		var text string
		switch row.Total.Type {
		case bson.TypeDecimal128:
			text = row.Total.Decimal128().String()
		case bson.TypeInt32:
			text = strconv.FormatInt(int64(row.Total.Int32()), 10)
		case bson.TypeInt64:
			text = strconv.FormatInt(row.Total.Int64(), 10)
		default:
			return fmt.Errorf("source money aggregate is not exact decimal/integer")
		}
		value, ok := new(big.Rat).SetString(text)
		if !ok {
			return fmt.Errorf("source money aggregate is not finite")
		}
		expected[moneyKey{row.Key.User, row.Key.Account, row.Key.Currency}] = value
	}
	if err = cursor.Err(); err != nil {
		return err
	}
	rows, err := m.target.Query(ctx, `SELECT j.user_id,COALESCE(p->>'account',''),COALESCE(p->'amount'->>'c',''),COALESCE(SUM((p->'amount'->>'v')::numeric),0)::text FROM journal j CROSS JOIN LATERAL jsonb_array_elements(COALESCE(NULLIF(j.postings,'null'::jsonb),'[]'::jsonb)) p GROUP BY j.user_id,p->>'account',p->'amount'->>'c'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var key moneyKey
		var text string
		if err = rows.Scan(&key.user, &key.account, &key.currency, &text); err != nil {
			return err
		}
		actual, ok := new(big.Rat).SetString(text)
		if !ok {
			return fmt.Errorf("target money aggregate is not finite")
		}
		source, found := expected[key]
		if !found || source.Cmp(actual) != 0 {
			return fmt.Errorf("exact posting totals differ between source and target")
		}
		delete(expected, key)
		count++
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if len(expected) > 0 {
		return fmt.Errorf("target is missing %d posting-total groups", len(expected))
	}
	fmt.Fprintf(m.out, "verified exact posting totals %d owner/account/currency groups\n", count)
	return nil
}
