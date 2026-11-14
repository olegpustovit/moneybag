package tables

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

func TestJoinedEntryAliasUsesReferenceIndex(t *testing.T) {
	engine, err := expressions.New()
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(nil, nil, engine)
	table := Table{ID: "allocations", Columns: []Column{{ID: "ledger_id", Type: "entry"}, {ID: "label", Type: "string"}}}
	p := principal.Principal{UserID: "01ARZ3NDEKTSV4RRFFQ69G5FAV"}
	for _, joined := range []bool{false, true} {
		filter, args, err := service.queryFilter(table, QueryInput{JoinEntry: joined, Where: `row.entry == 'entry-id' && row.label == 'allocation'`}, p)
		if err != nil {
			t.Fatal(err)
		}
		predicate, ok := args["values0"]
		if joined && args["column0"] != "ledger_id" {
			t.Fatal(args)
		}
		if ok != joined {
			t.Fatalf("joined=%v filter=%#v", joined, filter)
		}
		if joined && !reflect.DeepEqual(predicate, []string{"entry-id"}) {
			t.Fatal(filter)
		}
		if strings.Contains(filter, "cells.entry") {
			t.Fatal("indexed the alias rather than the stored column", filter)
		}
	}
}

func TestExplanationIDsRequireReadScope(t *testing.T) {
	service := NewService(nil, nil, nil)
	p := principal.Principal{UserID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Scopes: []string{"model:write"}}
	if _, err := service.ExplainedEntryIDs(context.Background(), p, "receipts"); !errors.Is(err, principal.ErrForbidden) {
		t.Fatalf("read scope not enforced: %v", err)
	}
}
