package semantic

import (
	"context"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"math"
	"math/big"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
)

// FactKinds are the server-computed facts a dashboard answer or metric_query can ask for.
var FactKinds = []string{"period_change", "top_contributors", "top_detractors", "estimate_share", "largest_entry"}

type Contributor struct {
	Name  string  `json:"name"`
	Delta string  `json:"delta"`
	Text  string  `json:"text"`
	Share float64 `json:"share"`
}

// Fact is plain, deterministic English; sentences never carry identifiers.
type Fact struct {
	Kind         string        `json:"kind"`
	Sentence     string        `json:"sentence"`
	Value        string        `json:"value,omitempty"`
	Text         string        `json:"text,omitempty"`
	Direction    string        `json:"direction,omitempty"`
	Good         bool          `json:"good"`
	Contributors []Contributor `json:"contributors,omitempty"`
}

// inSentence lowers a label's first letter unless it starts an acronym such as EUR.
func inSentence(label string) string {
	runes := []rune(label)
	if len(runes) > 1 && unicode.IsUpper(runes[0]) && unicode.IsUpper(runes[1]) {
		return label
	}
	if len(runes) > 0 {
		runes[0] = unicode.ToLower(runes[0])
	}
	return string(runes)
}

func periodWords(compare string) string {
	if compare == "previous_year" {
		return "the same period last year"
	}
	return "the previous period"
}

func signedText(unit string, v *big.Rat) string {
	if v.Sign() > 0 {
		return "+" + FormatValue(unit, v)
	}
	return FormatValue(unit, v)
}

func wholePercent(pct float64) string {
	pct = math.Abs(pct)
	if pct > 0 && pct < 1 {
		return "less than 1%"
	}
	return fmt.Sprintf("%.0f%%", pct)
}

func longDate(date string) string {
	t, e := time.Parse(day, date)
	if e != nil {
		return date
	}
	return t.Format("2 January 2006")
}

// facts computes each requested kind from the query's own period; comparisons default to the previous period.
func (s *Service) facts(ctx context.Context, p principal.Principal, b *book, m Metric, in QueryInput, req request, res Result) ([]Fact, error) {
	compare := in.Compare
	if compare == "" || compare == "none" {
		compare = "previous_period"
	}
	priorFrom, priorTo, e := ComparePeriod(compare, in.From, res.Entries.To)
	if e != nil {
		return nil, e
	}
	if in.From == "" {
		priorFrom, priorTo = "", ""
	}
	out := []Fact{}
	for _, kind := range in.Facts {
		switch kind {
		case "period_change":
			fact, e := s.periodChange(ctx, p, b, m, req, res, compare, priorFrom, priorTo)
			if e != nil {
				return nil, e
			}
			out = append(out, fact)
		case "top_contributors", "top_detractors":
			fact, e := s.movers(ctx, p, b, m, in, req, kind, compare, priorFrom, priorTo)
			if e != nil {
				return nil, e
			}
			out = append(out, fact)
		case "estimate_share":
			out = append(out, estimateShare(m, res))
		case "largest_entry":
			fact, e := s.largestEntry(ctx, p, b, m, req)
			if e != nil {
				return nil, e
			}
			out = append(out, fact)
		}
	}
	return out, nil
}

func (s *Service) periodChange(ctx context.Context, p principal.Principal, b *book, m Metric, req request, res Result, compare, priorFrom, priorTo string) (Fact, error) {
	now, _ := new(big.Rat).SetString(res.Total)
	if now == nil {
		now = new(big.Rat)
	}
	fact := Fact{Kind: "period_change", Value: res.Total, Text: res.Text, Direction: "flat", Good: true}
	if priorFrom == "" {
		fact.Sentence = fmt.Sprintf("%s was %s; there is no earlier period to compare it with.", m.Label, res.Text)
		return fact, nil
	}
	prior, e := s.aggregateMetric(ctx, p, b, m, request{from: priorFrom, to: priorTo, policy: req.policy, filters: req.filters, where: req.where})
	if e != nil {
		return Fact{}, e
	}
	before := folded(m, prior, priorFrom, priorTo)
	cmp := now.Cmp(before)
	fact.Direction, fact.Good = judge(m, cmp)
	switch {
	case cmp == 0:
		fact.Sentence = fmt.Sprintf("%s was unchanged at %s, compared with %s.", m.Label, res.Text, periodWords(compare))
	case before.Sign() == 0:
		fact.Sentence = fmt.Sprintf("%s rose to %s from nothing in %s.", m.Label, res.Text, periodWords(compare))
	default:
		ratio, _ := new(big.Rat).Quo(new(big.Rat).Sub(now, before), new(big.Rat).Abs(before)).Float64()
		verb := "rose"
		if cmp < 0 {
			verb = "fell"
		}
		fact.Sentence = fmt.Sprintf("%s %s %s to %s, compared with %s.", m.Label, verb, wholePercent(ratio*100), res.Text, periodWords(compare))
	}
	return fact, nil
}

// movers ranks the split's changes: contributors are the largest rises, detractors the largest falls.
func (s *Service) movers(ctx context.Context, p principal.Principal, b *book, m Metric, in QueryInput, req request, kind, compare, priorFrom, priorTo string) (Fact, error) {
	dimension := in.Split
	if dimension == "" {
		dimension = "category"
		if len(m.Dimensions) > 0 {
			dimension = m.Dimensions[0]
		}
	}
	rising := kind == "top_contributors"
	direction, good := judge(m, map[bool]int{true: 1, false: -1}[rising])
	fact := Fact{Kind: kind, Direction: direction, Good: good, Contributors: []Contributor{}}
	if priorFrom == "" {
		fact.Sentence = "There is no earlier period to compare " + inSentence(m.Label) + " with."
		return fact, nil
	}
	now, e := s.aggregateMetric(ctx, p, b, m, request{from: req.from, to: req.to, split: dimension, policy: req.policy, filters: req.filters, where: req.where})
	if e != nil {
		return Fact{}, e
	}
	then, e := s.aggregateMetric(ctx, p, b, m, request{from: priorFrom, to: priorTo, split: dimension, policy: req.policy, filters: req.filters, where: req.where})
	if e != nil {
		return Fact{}, e
	}
	names := make([]string, 0, len(now.names)+len(then.names))
	for name := range now.names {
		names = append(names, name)
	}
	for name := range then.names {
		if now.names[name] == nil {
			names = append(names, name)
		}
	}
	deltas := map[string]*big.Rat{}
	absolute := new(big.Rat)
	for _, name := range names {
		delta := new(big.Rat)
		if v := now.names[name]; v != nil {
			delta.Add(delta, v)
		}
		if v := then.names[name]; v != nil {
			delta.Sub(delta, v)
		}
		if m.Kind == "rate" || m.Additivity == "avg" {
			delta.Quo(delta, monthsBetween(req.from, req.to))
		}
		deltas[name] = delta
		absolute.Add(absolute, new(big.Rat).Abs(delta))
	}
	picked := slices.DeleteFunc(slices.Clone(names), func(name string) bool {
		sign := deltas[name].Sign()
		return rising && sign <= 0 || !rising && sign >= 0
	})
	sort.Slice(picked, func(i, j int) bool {
		x, y := new(big.Rat).Abs(deltas[picked[i]]), new(big.Rat).Abs(deltas[picked[j]])
		if c := x.Cmp(y); c != 0 {
			return c > 0
		}
		return picked[i] < picked[j]
	})
	if len(picked) > 5 {
		picked = picked[:5]
	}
	for _, name := range picked {
		share, _ := new(big.Rat).Quo(new(big.Rat).Abs(deltas[name]), absolute).Float64()
		fact.Contributors = append(fact.Contributors, Contributor{name, decimalText(deltas[name]), signedText(m.Unit, deltas[name]), math.Round(share*1000) / 10})
	}
	if len(picked) == 0 {
		word := "rose"
		if !rising {
			word = "fell"
		}
		fact.Sentence = fmt.Sprintf("Nothing in %s %s, compared with %s.", inSentence(m.Label), word, periodWords(compare))
		return fact, nil
	}
	top := fact.Contributors[0]
	fact.Value, fact.Text = top.Delta, top.Text
	magnitude := FormatValue(m.Unit, new(big.Rat).Abs(deltas[top.Name]))
	if rising {
		fact.Sentence = fmt.Sprintf("%s rose the most, up %s compared with %s.", top.Name, magnitude, periodWords(compare))
	} else {
		fact.Sentence = fmt.Sprintf("%s fell the most, down %s compared with %s.", top.Name, magnitude, periodWords(compare))
	}
	return fact, nil
}

func estimateShare(m Metric, res Result) Fact {
	fact := Fact{Kind: "estimate_share", Value: res.Estimates.Total, Text: res.Estimates.Text, Good: res.Estimates.SharePct == 0}
	label := inSentence(m.Label)
	switch {
	case res.Estimates.SharePct == 0:
		fact.Sentence = "None of " + label + " is estimated."
	case res.Estimates.Policy == "exclude":
		fact.Sentence = fmt.Sprintf("Estimates are left out of %s; they would add %s.", label, res.Estimates.Text)
	default:
		fact.Sentence = fmt.Sprintf("Estimates make up %s of %s.", wholePercent(res.Estimates.SharePct), label)
	}
	return fact
}

func (s *Service) largestEntry(ctx context.Context, p principal.Principal, b *book, m Metric, req request) (Fact, error) {
	flow := m
	flow.Kind, flow.Additivity = "flow", "sum"
	a, e := s.aggregateMetric(ctx, p, b, flow, request{from: req.from, to: req.to, policy: req.policy, filters: req.filters, where: req.where})
	if e != nil {
		return Fact{}, e
	}
	fact := Fact{Kind: "largest_entry", Good: true}
	if a.largest == nil {
		fact.Sentence = "There are no entries in this period."
		return fact, nil
	}
	fact.Value, fact.Text = decimalText(a.largestValue), FormatValue(m.Unit, new(big.Rat).Abs(a.largestValue))
	at := ""
	if payee := strings.TrimSpace(a.largest.name); payee != "" {
		at = " at " + payee
	}
	fact.Sentence = fmt.Sprintf("The largest entry was %s%s on %s.", fact.Text, at, longDate(a.largest.date))
	return fact, nil
}
