package tables

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
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
		filter, err := service.queryFilter(table, QueryInput{JoinEntry: joined, Where: `row.entry == 'entry-id' && row.label == 'allocation'`}, p)
		if err != nil {
			t.Fatal(err)
		}
		predicate, ok := filter["cells.ledger_id"]
		if ok != joined {
			t.Fatalf("joined=%v filter=%#v", joined, filter)
		}
		if joined && !reflect.DeepEqual(predicate, bson.M{"$in": []any{"entry-id"}}) {
			t.Fatal(filter)
		}
		if _, ok := filter["cells.entry"]; ok {
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
