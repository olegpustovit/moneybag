package tables

import (
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"testing"
)

func TestReceiptTemplateDefinitions(t *testing.T) {
	engine, err := expressions.New()
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(nil, nil, engine)
	known := map[string]bool{}
	for _, table := range Templates()[0].Tables {
		if err := service.validateColumns(table.Columns); err != nil {
			t.Fatalf("%s: %v", table.ID, err)
		}
		if err := service.validateDefinition(table); err != nil {
			t.Fatalf("%s: %v", table.ID, err)
		}
		for _, column := range table.Columns {
			if column.Type == "ref" && !known[column.Table] {
				t.Fatalf("%s references uncreated table %s", table.ID, column.Table)
			}
		}
		known[table.ID] = true
	}
	if len(known) != 5 {
		t.Fatal(known)
	}
}
