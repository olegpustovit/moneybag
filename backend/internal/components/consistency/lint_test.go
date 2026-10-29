package consistency

import (
	"strings"
	"testing"

	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
)

func TestScanText(t *testing.T) {
	cases := map[string]string{
		"'6 pots'":                 "6",
		"net 4,057.40 today":       "4057.40",
		"since 2026-01-05 x2 a.5":  "",
		"0 1 100 12 26 52 eurusd2": "",
		"limit * 240":              "240",
	}
	for in, want := range cases {
		got := ""
		for _, l := range ScanText(in) {
			got += l.Literal + ","
		}
		if strings.TrimSuffix(got, ",") != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestSpecLiteralsAndOrphans(t *testing.T) {
	spec := dashboard.Spec{ID: "d", Title: "Plan 77", Data: map[string]dashboard.Query{"base": {From: "journal"}, "used": {From: "query:base"}, "idle": {From: "journal"}}, Computed: map[string]string{"c": "data.used.size()"},
		Components: []dashboard.Component{{ID: "k_n", Type: "kpi", Value: "computed.c", Sub: "'6 pots'", Rows: "gone"}}}
	lits := SpecLiterals(spec)
	if len(lits) != 2 {
		t.Fatalf("%+v", lits)
	}
	var sub Literal
	for _, l := range lits {
		if l.ComponentID == "k_n" {
			sub = l
		}
	}
	if sub.Field != "sub" || sub.Literal != "6" {
		t.Fatalf("%+v", sub)
	}
	got := strings.Join(SpecOrphans(spec, nil, nil), "|")
	for _, want := range []string{"data.idle is never referenced", "missing query gone"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "data.used is") || strings.Contains(got, "data.base is") || strings.Contains(got, "computed.c is") {
		t.Errorf("false orphan: %s", got)
	}
}

func TestDuplicates(t *testing.T) {
	inputs := []Input{{Name: "stream a", Value: 1234.5}, {Name: "rate", Value: 1.234567, Exact: true}, {Name: "horizon", Value: 12}}
	lits := []Literal{{Literal: "1234.5", Value: 1234.5}, {Literal: "1.23", Value: 1.23}, {Literal: "1.234567", Value: 1.234567}, {Literal: "999", Value: 999}}
	d := Duplicates(inputs, lits, func(l Literal) string { return "w" })
	if len(d) != 2 || d[0].Input != "stream a" || d[1].Input != "rate" {
		t.Fatalf("%+v", d)
	}
}
