package mcpserver

import (
	"reflect"
	"testing"

	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
)

func TestSummarizeDashboardListsLayoutComponents(t *testing.T) {
	spec := dashboard.Spec{
		ID:    "d1",
		Title: "Cash",
		Data:  map[string]dashboard.Query{"ledger": {From: "entries"}},
		Pages: []dashboard.Page{{ID: "main", Title: "Overview", Layout: []dashboard.Row{
			{Cols: []dashboard.Placement{{Span: 6, Component: "k1"}, {Span: 6, Component: "t1"}}},
			{Cols: []dashboard.Placement{{Span: 12, Component: "c1"}}},
		}}},
		Components: []dashboard.Component{{ID: "k1", Type: "kpi", Title: "Net", Format: "usd0"}},
	}
	out := summarizeDashboard(dashboard.GetResult{Spec: spec, Version: 3, Status: "draft"})
	if out.ID != "d1" || out.Version != 3 || out.Status != "draft" {
		t.Fatalf("header: %+v", out)
	}
	if !reflect.DeepEqual(out.Sources, []string{"ledger"}) {
		t.Fatalf("sources: %v", out.Sources)
	}
	if !reflect.DeepEqual(out.Pages[0].Components, []string{"k1", "t1", "c1"}) {
		t.Fatalf("page components: %v", out.Pages[0].Components)
	}
	if len(out.Components) != 1 || out.Components[0].Format != "usd0" {
		t.Fatalf("components: %+v", out.Components)
	}
}
