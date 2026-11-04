package mcpserver

import (
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/tables"
)

type entriesListInput struct {
	ledger.EntriesInput
	ExplainedBy string `json:"explained_by,omitempty" jsonschema:"table id with explaining rows, or !table id for entries without rows"`
}
type entryDetails struct {
	Entry       ledger.Entry         `json:"entry"`
	Links       []ledger.Link        `json:"links"`
	Audit       []audit.Event        `json:"audit"`
	ExplainedBy []tables.Explanation `json:"explained_by,omitempty"`
}
