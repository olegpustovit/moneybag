package semantic

import (
	"cel.dev/cel-go/cel"
	"context"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/rates"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/reqcache"
	"math"
	"math/big"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Filter struct {
	Field  string   `json:"field" jsonschema:"a dimension from catalog_get or field_values"`
	Op     string   `json:"op" jsonschema:"in, not_in or prefix"`
	Values []string `json:"values"`
}
type QueryInput struct {
	Metric    string   `json:"metric"`
	From      string   `json:"from,omitempty" jsonschema:"YYYY-MM-DD; omitted starts at the first entry"`
	To        string   `json:"to,omitempty" jsonschema:"YYYY-MM-DD; omitted ends today"`
	Compare   string   `json:"compare,omitempty" jsonschema:"previous_period, previous_year or none"`
	Split     string   `json:"split,omitempty" jsonschema:"dimension to break the series down by"`
	Bucket    string   `json:"bucket,omitempty" jsonschema:"day, week, month or auto; omitted returns no time buckets"`
	Estimates string   `json:"estimates,omitempty" jsonschema:"include, exclude or split; omitted uses the metric's policy"`
	Filters   []Filter `json:"filters,omitempty"`
	Facts     []string `json:"facts,omitempty" jsonschema:"period_change, top_contributors, top_detractors, estimate_share, largest_entry"`
}
type Point struct {
	Bucket    string `json:"bucket,omitempty"`
	Name      string `json:"name"`
	Value     string `json:"value"`
	Text      string `json:"text"`
	Estimated bool   `json:"estimated"`
}
type Comparison struct {
	From      string   `json:"from"`
	To        string   `json:"to"`
	Total     string   `json:"total"`
	Text      string   `json:"text"`
	DeltaPct  *float64 `json:"delta_pct"`
	Direction string   `json:"direction"`
	Good      bool     `json:"good"`
}
type EstimateInfo struct {
	Policy   string  `json:"policy"`
	Total    string  `json:"total"`
	Text     string  `json:"text"`
	SharePct float64 `json:"share_pct"`
}
type EntriesRef struct {
	From     string   `json:"from"`
	To       string   `json:"to"`
	Accounts []string `json:"accounts"`
	Exclude  []string `json:"exclude,omitempty"`
}
type Result struct {
	Metric    string       `json:"metric"`
	Label     string       `json:"label"`
	Unit      string       `json:"unit"`
	Total     string       `json:"total"`
	Text      string       `json:"text"`
	Compare   *Comparison  `json:"compare,omitempty"`
	Series    []Point      `json:"series"`
	Estimates EstimateInfo `json:"estimates"`
	Formula   string       `json:"formula"`
	Entries   EntriesRef   `json:"entries"`
	Facts     []Fact       `json:"facts,omitempty"`
	Truncated bool         `json:"truncated,omitempty"`
	Message   string       `json:"message,omitempty"`
}

// maxSeriesRows keeps the default response under the agent token budget.
const (
	maxSeriesRows = 500
	maxSeriesName = 20
)

type row struct {
	date, entry, kind, payee, name, account, commodity string
	seq                                                int64
	tags                                               []string
	amount                                             ledger.Decimal
	price                                              *ledger.Amount
	estimated                                          bool
	acct                                               ledger.Account
}

// book is one request's view of the posted ledger, sorted by date, with display names resolved.
type book struct {
	rows     []row
	accounts map[string]ledger.Account
	labels   map[string]string
	first    string
	mu       sync.Mutex
	rateMemo map[string]*big.Rat
}

// Dimensions any metric can split by; other names resolve as account group keys.
var Dimensions = []string{"category", "type", "source", "pot", "class", "account", "payee", "trip", "tag"}

func (s *Service) book(ctx context.Context, p principal.Principal) (*book, error) {
	return reqcache.Do(ctx, "semantic-book|"+p.UserID, func() (*book, error) {
		b := &book{accounts: map[string]ledger.Account{}, rateMemo: map[string]*big.Rat{}}
		in := ledger.AccountsInput{IncludeClosed: true, Limit: 500}
		for {
			page, e := s.ledger.Accounts(ctx, p, in)
			if e != nil {
				return nil, e
			}
			for _, a := range page.Items {
				b.accounts[a.ID] = a
			}
			if page.NextCursor == "" {
				break
			}
			in.Cursor = page.NextCursor
		}
		var e error
		if b.labels, e = s.Labels(ctx, p); e != nil {
			return nil, e
		}
		aliases, e := list[PayeeAlias](ctx, s.repo.db.Collection("payee_aliases"), p.UserID, "pattern")
		if e != nil {
			return nil, e
		}
		patterns := make([]*regexp.Regexp, len(aliases))
		for i, alias := range aliases {
			if patterns[i], e = regexp.Compile("(?i)" + alias.Pattern); e != nil {
				return nil, fmt.Errorf("payee alias %s: %w", alias.Name, e)
			}
		}
		names := map[string]string{}
		display := func(payee string) string {
			if name, ok := names[payee]; ok {
				return name
			}
			name := payee
			for i, pattern := range patterns {
				if pattern.MatchString(payee) {
					name = aliases[i].Name
					break
				}
			}
			names[payee] = name
			return name
		}
		e = s.ledger.PostedStream(ctx, p, "", "", true, func(entry ledger.Entry) error {
			for _, post := range entry.Postings {
				a := b.accounts[post.Account]
				b.rows = append(b.rows, row{date: entry.Date, entry: entry.ID, kind: entry.Kind, payee: entry.Payee, name: display(entry.Payee), account: post.Account, commodity: post.Amount.Commodity, seq: entry.Seq, tags: entry.Tags, amount: post.Amount.Value, price: post.Price, estimated: a.IsEstimated(), acct: a})
			}
			return nil
		})
		if e != nil {
			return nil, e
		}
		sort.SliceStable(b.rows, func(i, j int) bool {
			if b.rows[i].date != b.rows[j].date {
				return b.rows[i].date < b.rows[j].date
			}
			return b.rows[i].seq < b.rows[j].seq
		})
		if len(b.rows) > 0 {
			b.first = b.rows[0].date
		}
		return b, nil
	})
}

func (b *book) label(kind, key, fallback string) string {
	if label, ok := b.labels[kind+":"+key]; ok {
		return label
	}
	return fallback
}

var machineName = regexp.MustCompile(`[_:]|^[a-z0-9-]+$`)

// humanize keeps raw keys off the screen until an agent names them with label_set.
func humanize(key string) string {
	if !machineName.MatchString(key) {
		return key
	}
	words := strings.Fields(strings.NewReplacer("_", " ", ":", " ", "-", " ").Replace(key))
	text := strings.Join(words, " ")
	if text == "" {
		return key
	}
	return strings.ToUpper(text[:1]) + text[1:]
}

// values returns a row's values for a dimension; trip and tag can hold several.
func (b *book) values(r *row, field string) []string {
	switch field {
	case "account":
		return []string{r.account}
	case "payee":
		return []string{r.name}
	case "pot":
		return []string{r.acct.Pot}
	case "class":
		return []string{r.acct.Class}
	case "tag":
		return r.tags
	case "trip":
		out := []string{}
		for _, tag := range r.tags {
			if name, ok := strings.CutPrefix(tag, "trip:"); ok {
				out = append(out, name)
			}
		}
		return out
	case "category":
		if value := r.acct.Groups["category"]; value != "" {
			return []string{value}
		}
		return []string{b.accountLabel(r.acct, r.account)}
	}
	return []string{r.acct.Groups[field]}
}

func (b *book) accountLabel(a ledger.Account, id string) string {
	fallback := a.Label()
	if fallback == "" {
		fallback = humanize(id)
	}
	return b.label("account", id, fallback)
}

// display is the human name of a dimension value.
func (b *book) display(field, value string) string {
	switch field {
	case "account":
		return b.accountLabel(b.accounts[value], value)
	case "payee":
		return value
	case "trip":
		return b.label("tag", "trip:"+value, humanize(value))
	case "tag":
		return b.label("tag", value, humanize(value))
	case "category":
		return b.label("category", value, humanize(value))
	case "pot":
		return b.label("pot", value, humanize(value))
	}
	return b.label("dimension", field+":"+value, humanize(value))
}

func noValueName(field string) string {
	switch field {
	case "trip":
		return "No trip"
	case "tag":
		return "Untagged"
	}
	return "Uncategorized"
}

func (b *book) checkField(field string) error {
	if slices.Contains(Dimensions, field) {
		return nil
	}
	for _, a := range b.accounts {
		if _, ok := a.Groups[field]; ok {
			return nil
		}
	}
	return fmt.Errorf("unknown dimension %q; use %s or an account group key", field, strings.Join(Dimensions, ", "))
}

// amountIn values a row in the unit: money at the entry date's rate, otherwise its native amount.
func (s *Service) amountIn(ctx context.Context, p principal.Principal, b *book, r *row, unit string) (*big.Rat, error) {
	switch unit {
	case "eur", "usd":
		b.mu.Lock()
		defer b.mu.Unlock()
		v, e := s.rates.ValueAt(ctx, p, ledger.Posting{Account: r.account, Amount: ledger.Amount{Value: r.amount, Commodity: r.commodity}, Price: r.price}, strings.ToUpper(unit), r.date, b.rateMemo)
		if e != nil {
			return nil, fmt.Errorf("cannot value %s on %s: %w", r.commodity, r.date, e)
		}
		return v.Rat(), nil
	case "int":
		return big.NewRat(1, 1), nil
	}
	return r.amount.Rat(), nil
}

type request struct {
	from, to, split, bucket, policy string
	filters                         []Filter
	where                           string
}
type cellKey struct {
	bucket, name string
	estimated    bool
}
type aggregate struct {
	total, estimated, lastMonth *big.Rat
	cells                       map[cellKey]*big.Rat
	names                       map[string]*big.Rat
	largest                     *row
	largestValue                *big.Rat
	count                       int
}

func hasPrefixAny(id string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(id, prefix) {
			return true
		}
	}
	return false
}

func checkFilters(b *book, filters []Filter) error {
	if len(filters) > 20 {
		return fmt.Errorf("at most 20 filters")
	}
	for _, f := range filters {
		if e := oneOf("filter op", f.Op, "in", "not_in", "prefix"); e != nil {
			return e
		}
		if e := b.checkField(f.Field); e != nil {
			return e
		}
		if len(f.Values) == 0 || len(f.Values) > 200 {
			return fmt.Errorf("filter on %s needs 1..200 values", f.Field)
		}
	}
	return nil
}

func (b *book) passes(r *row, filters []Filter) bool {
	for _, f := range filters {
		values := b.values(r, f.Field)
		hit := false
		for _, value := range values {
			switch f.Op {
			case "prefix":
				hit = hit || hasPrefixAny(value, f.Values)
			default:
				hit = hit || slices.Contains(f.Values, value)
			}
		}
		if f.Op == "not_in" {
			hit = !hit
		}
		if !hit {
			return false
		}
	}
	return true
}

type wherePrograms []struct {
	src     string
	program cel.Program
}

func (s *Service) compileWhere(sources ...string) (wherePrograms, error) {
	var out wherePrograms
	for _, src := range sources {
		if src == "" {
			continue
		}
		program, e := s.expr.Compile(src)
		if e != nil {
			return nil, fmt.Errorf("where: %w", e)
		}
		out = append(out, struct {
			src     string
			program cel.Program
		}{src, program})
	}
	return out, nil
}

func (s *Service) passesWhere(ctx context.Context, p principal.Principal, b *book, r *row, programs wherePrograms) (bool, error) {
	if len(programs) == 0 {
		return true, nil
	}
	tags := make([]any, len(r.tags))
	for i, tag := range r.tags {
		tags[i] = tag
	}
	groups := make(map[string]any, len(r.acct.Groups))
	for k, v := range r.acct.Groups {
		groups[k] = v
	}
	vars := map[string]any{"date": r.date, "entry": r.entry, "seq": float64(r.seq), "kind": r.kind, "payee": r.payee, "tags": tags, "account": r.account, "class": r.acct.Class, "groups": groups, "commodity": r.commodity, "amount": r.amount.Float(), "estimated": r.estimated}
	for _, item := range programs {
		if strings.Contains(item.src, "value_") {
			for field, unit := range map[string]string{"value_eur": "eur", "value_usd": "usd"} {
				if _, done := vars[field]; !done && strings.Contains(item.src, field) {
					v, e := s.amountIn(ctx, p, b, r, unit)
					if e != nil {
						return false, e
					}
					value, _ := v.Float64()
					vars[field] = value
				}
			}
		}
		out, e := expressions.EvalProgramRow(ctx, item.src, item.program, nil, vars)
		if e != nil {
			return false, fmt.Errorf("where: %w", e)
		}
		ok, isBool := out.Value.(bool)
		if !isBool {
			return false, fmt.Errorf("where must produce boolean")
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func bucketOf(date, bucket string) string {
	switch bucket {
	case "month":
		return date[:7]
	case "week":
		t, e := time.Parse(day, date)
		if e != nil {
			return date
		}
		return t.AddDate(0, 0, -(int(t.Weekday())+6)%7).Format(day)
	case "day":
		return date
	}
	return ""
}

func autoBucket(from, to string) string {
	a, e1 := time.Parse(day, from)
	b, e2 := time.Parse(day, to)
	if e1 != nil || e2 != nil {
		return "month"
	}
	switch days := b.Sub(a).Hours() / 24; {
	case days <= 45:
		return "day"
	case days <= 200:
		return "week"
	}
	return "month"
}

func (s *Service) aggregateMetric(ctx context.Context, p principal.Principal, b *book, m Metric, in request) (aggregate, error) {
	if m.Kind == "balance" {
		return s.aggregateBalance(ctx, p, b, m, in)
	}
	out := aggregate{total: new(big.Rat), estimated: new(big.Rat), lastMonth: new(big.Rat), cells: map[cellKey]*big.Rat{}, names: map[string]*big.Rat{}}
	programs, e := s.compileWhere(m.Where, in.where)
	if e != nil {
		return out, e
	}
	sign := big.NewRat(int64(m.Sign), 1)
	for i := range b.rows {
		r := &b.rows[i]
		if i%1000 == 0 && ctx.Err() != nil {
			return out, ctx.Err()
		}
		if r.date > in.to || r.date < in.from {
			continue
		}
		if len(m.Accounts) > 0 && !hasPrefixAny(r.account, m.Accounts) || hasPrefixAny(r.account, m.Exclude) || !b.passes(r, in.filters) {
			continue
		}
		ok, e := s.passesWhere(ctx, p, b, r, programs)
		if e != nil {
			return out, e
		}
		if !ok {
			continue
		}
		v, e := s.amountIn(ctx, p, b, r, m.Unit)
		if e != nil {
			return out, e
		}
		v = new(big.Rat).Mul(v, sign)
		if r.estimated {
			out.estimated.Add(out.estimated, v)
			if in.policy == "exclude" {
				continue
			}
		}
		out.total.Add(out.total, v)
		out.count++
		if r.date[:7] == in.to[:7] {
			out.lastMonth.Add(out.lastMonth, v)
		}
		if abs := new(big.Rat).Abs(v); out.largestValue == nil || abs.Cmp(new(big.Rat).Abs(out.largestValue)) > 0 {
			out.largest, out.largestValue = r, v
		}
		out.addCell(b, r, in, bucketOf(r.date, in.bucket), v)
	}
	return out, nil
}

func (a *aggregate) addCell(b *book, r *row, in request, bucket string, v *big.Rat) {
	names := []string{""}
	if in.split != "" {
		names = names[:0]
		for _, value := range b.values(r, in.split) {
			if value != "" {
				names = append(names, b.display(in.split, value))
			}
		}
		if len(names) == 0 {
			names = append(names, noValueName(in.split))
		}
	}
	for _, name := range names {
		key := cellKey{bucket, name, in.policy == "split" && r.estimated}
		if a.cells[key] == nil {
			a.cells[key] = new(big.Rat)
		}
		a.cells[key].Add(a.cells[key], v)
		if a.names[name] == nil {
			a.names[name] = new(big.Rat)
		}
		a.names[name].Add(a.names[name], v)
	}
}

// aggregateBalance values holdings at each bucket end, at that date's rate.
func (s *Service) aggregateBalance(ctx context.Context, p principal.Principal, b *book, m Metric, in request) (aggregate, error) {
	out := aggregate{total: new(big.Rat), estimated: new(big.Rat), lastMonth: new(big.Rat), cells: map[cellKey]*big.Rat{}, names: map[string]*big.Rat{}}
	programs, e := s.compileWhere(m.Where, in.where)
	if e != nil {
		return out, e
	}
	type holding struct {
		key       cellKey
		commodity string
		estimated bool
	}
	holdings := map[holding]*big.Rat{}
	ends := bucketEnds(in.from, in.to, in.bucket)
	sign := big.NewRat(int64(m.Sign), 1)
	next := 0
	value := func(h holding, amount *big.Rat, end string) (*big.Rat, error) {
		switch m.Unit {
		case "eur", "usd":
			rate, e := s.rates.Rate(ctx, p, h.commodity, strings.ToUpper(m.Unit), end+"T23:59:59Z")
			if e != nil {
				return nil, fmt.Errorf("cannot value %s on %s: %w", h.commodity, end, e)
			}
			d, e := ledger.DecimalFromRat(amount)
			if e != nil {
				return nil, e
			}
			v, e := rates.Apply(ledger.Amount{Value: d, Commodity: h.commodity}, rate, strings.ToUpper(m.Unit))
			if e != nil {
				return nil, e
			}
			return new(big.Rat).Mul(v.Value.Rat(), sign), nil
		}
		return new(big.Rat).Mul(amount, sign), nil
	}
	flush := func(end string, final bool) error {
		for h, amount := range holdings {
			if amount.Sign() == 0 {
				continue
			}
			v, e := value(h, amount, end)
			if e != nil {
				return e
			}
			key := h.key
			key.bucket = bucketOf(end, in.bucket)
			if final {
				if h.estimated {
					out.estimated.Add(out.estimated, v)
				}
				if !h.estimated || in.policy != "exclude" {
					out.total.Add(out.total, v)
				}
			}
			if h.estimated && in.policy == "exclude" {
				continue
			}
			if out.cells[key] == nil {
				out.cells[key] = new(big.Rat)
			}
			out.cells[key].Add(out.cells[key], v)
			if final {
				if out.names[key.name] == nil {
					out.names[key.name] = new(big.Rat)
				}
				out.names[key.name].Add(out.names[key.name], v)
			}
		}
		return nil
	}
	for i := range b.rows {
		r := &b.rows[i]
		if r.date > in.to {
			break
		}
		for next < len(ends) && r.date > ends[next] {
			if e := flush(ends[next], ends[next] == in.to); e != nil {
				return out, e
			}
			next++
		}
		if len(m.Accounts) > 0 && !hasPrefixAny(r.account, m.Accounts) || hasPrefixAny(r.account, m.Exclude) || !b.passes(r, in.filters) {
			continue
		}
		ok, e := s.passesWhere(ctx, p, b, r, programs)
		if e != nil {
			return out, e
		}
		if !ok {
			continue
		}
		out.count++
		names := []string{""}
		if in.split != "" {
			names = names[:0]
			for _, value := range b.values(r, in.split) {
				if value != "" {
					names = append(names, b.display(in.split, value))
				}
			}
			if len(names) == 0 {
				names = append(names, noValueName(in.split))
			}
		}
		for _, name := range names {
			h := holding{cellKey{"", name, in.policy == "split" && r.estimated}, r.commodity, r.estimated}
			if holdings[h] == nil {
				holdings[h] = new(big.Rat)
			}
			if m.Unit == "int" {
				holdings[h].Add(holdings[h], big.NewRat(1, 1))
			} else {
				holdings[h].Add(holdings[h], r.amount.Rat())
			}
		}
	}
	for ; next < len(ends); next++ {
		if e := flush(ends[next], ends[next] == in.to); e != nil {
			return out, e
		}
	}
	return out, nil
}

// bucketEnds lists the date of each bucket's valuation in order; the last is always to.
func bucketEnds(from, to, bucket string) []string {
	if bucket == "" || from == "" {
		return []string{to}
	}
	start, e1 := time.Parse(day, from)
	end, e2 := time.Parse(day, to)
	if e1 != nil || e2 != nil {
		return []string{to}
	}
	out := []string{}
	for t := start; !t.After(end); {
		var last time.Time
		switch bucket {
		case "day":
			last = t
		case "week":
			last = t.AddDate(0, 0, 6-(int(t.Weekday())+6)%7)
		default:
			last = time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, time.UTC)
		}
		if last.After(end) {
			last = end
		}
		out = append(out, last.Format(day))
		t = last.AddDate(0, 0, 1)
	}
	return out
}

func decimalText(r *big.Rat) string {
	if d, e := ledger.DecimalFromRat(r); e == nil {
		return d.String()
	}
	return strings.TrimRight(strings.TrimRight(r.FloatString(8), "0"), ".")
}

func grouped(r *big.Rat, decimals int) string {
	text := new(big.Rat).Abs(r).FloatString(decimals)
	whole, fraction, has := strings.Cut(text, ".")
	for i := len(whole) - 3; i > 0; i -= 3 {
		whole = whole[:i] + "," + whole[i:]
	}
	if has {
		whole += "." + fraction
	}
	if r.Sign() < 0 && strings.Trim(whole, "0.,") != "" {
		return "−" + whole
	}
	return whole
}

// FormatValue is the one place a metric value becomes display text.
func FormatValue(unit string, r *big.Rat) string {
	switch unit {
	case "eur", "usd":
		symbol := map[string]string{"eur": "€", "usd": "$"}[unit]
		text := grouped(r, 2)
		if rest, negative := strings.CutPrefix(text, "−"); negative {
			return "−" + symbol + rest
		}
		return symbol + text
	case "pct":
		return grouped(r, 1) + "%"
	case "mo":
		return grouped(r, 1) + " mo"
	case "days":
		if new(big.Rat).Abs(r).Cmp(big.NewRat(1, 1)) == 0 {
			return grouped(r, 0) + " day"
		}
		return grouped(r, 0) + " days"
	}
	return grouped(r, 0)
}

func monthsBetween(from, to string) int64 {
	a, e1 := time.Parse(day, from)
	b, e2 := time.Parse(day, to)
	if e1 != nil || e2 != nil {
		return 1
	}
	return max(1, int64((b.Year()-a.Year())*12+int(b.Month())-int(a.Month())+1))
}

// folded applies the metric's fold over time to a flow aggregate; balances already hold the period-end value.
func folded(m Metric, a aggregate, from, to string) *big.Rat {
	if m.Kind == "balance" {
		return a.total
	}
	if m.Kind == "rate" || m.Additivity == "avg" {
		return new(big.Rat).Quo(a.total, big.NewRat(monthsBetween(from, to), 1))
	}
	if m.Additivity == "last" {
		return a.lastMonth
	}
	return a.total
}

func rounded(m Metric, v *big.Rat) string {
	if m.Kind == "rate" || m.Additivity == "avg" {
		return v.FloatString(2)
	}
	return decimalText(v)
}

// lowerIsGood reads polarity from the ledger's signs: spending is positive and bad to grow, while income, assets and
// debts are only "lower is better" when the metric's sign has turned them around. Mixed accounts count as higher is better.
func lowerIsGood(m Metric) bool {
	lower := len(m.Accounts) > 0
	for _, prefix := range m.Accounts {
		lower = lower && strings.HasPrefix(prefix, "expenses:") == (m.Sign != -1)
	}
	return lower
}

func judge(m Metric, delta int) (string, bool) {
	switch {
	case delta > 0:
		return "up", !lowerIsGood(m)
	case delta < 0:
		return "down", lowerIsGood(m)
	}
	return "flat", true
}

func (s *Service) Query(ctx context.Context, p principal.Principal, in QueryInput) (Result, error) {
	return s.queryWhere(ctx, p, in, "")
}

// queryWhere narrows a query with one more CEL filter, for drill-downs.
func (s *Service) queryWhere(ctx context.Context, p principal.Principal, in QueryInput, where string) (Result, error) {
	if e := p.Require("ledger:read"); e != nil {
		return Result{}, e
	}
	ctx = reqcache.With(rates.WithQuoteCache(ctx))
	m, e := s.Metric(ctx, p, in.Metric)
	if e != nil {
		return Result{}, e
	}
	b, e := s.book(ctx, p)
	if e != nil {
		return Result{}, e
	}
	return s.run(ctx, p, b, m, in, where)
}

func (s *Service) run(ctx context.Context, p principal.Principal, b *book, m Metric, in QueryInput, where string) (Result, error) {
	from, to := in.From, in.To
	if to == "" {
		to = time.Now().UTC().Format(day)
	}
	if from == "" {
		from = b.first
		if from == "" || from > to {
			from = to
		}
	}
	if !validDate(from) || !validDate(to) || from > to {
		return Result{}, fmt.Errorf("from and to must be YYYY-MM-DD with from at or before to")
	}
	if e := oneOf("bucket", in.Bucket, "", "day", "week", "month", "auto"); e != nil {
		return Result{}, e
	}
	if e := oneOf("estimates", in.Estimates, "", "include", "exclude", "split"); e != nil {
		return Result{}, e
	}
	for _, kind := range in.Facts {
		if !slices.Contains(FactKinds, kind) {
			return Result{}, fmt.Errorf("facts must be among %s", strings.Join(FactKinds, ", "))
		}
	}
	if in.Split != "" {
		if e := b.checkField(in.Split); e != nil {
			return Result{}, e
		}
		if len(m.Dimensions) > 0 && !slices.Contains(m.Dimensions, in.Split) {
			return Result{}, fmt.Errorf("metric %s does not split by %s; use %s", m.ID, in.Split, strings.Join(m.Dimensions, ", "))
		}
	}
	if e := checkFilters(b, in.Filters); e != nil {
		return Result{}, e
	}
	compareFrom, compareTo, e := ComparePeriod(in.Compare, from, to)
	if e != nil {
		return Result{}, e
	}
	policy := in.Estimates
	if policy == "" {
		policy = m.Estimates
	}
	bucket := in.Bucket
	if bucket == "auto" {
		bucket = autoBucket(from, to)
	}
	req := request{from: from, to: to, split: in.Split, bucket: bucket, policy: policy, filters: in.Filters, where: where}
	a, e := s.aggregateMetric(ctx, p, b, m, req)
	if e != nil {
		return Result{}, e
	}
	total := folded(m, a, from, to)
	res := Result{Metric: m.ID, Label: m.Label, Unit: m.Unit, Total: rounded(m, total), Text: FormatValue(m.Unit, total), Series: []Point{}, Formula: formula(b, m, policy), Entries: EntriesRef{From: from, To: to, Accounts: slices.Clone(m.Accounts), Exclude: slices.Clone(m.Exclude)}}
	if res.Entries.Accounts == nil {
		res.Entries.Accounts = []string{}
	}
	share := 0.0
	whole := new(big.Rat).Set(a.total)
	if policy == "exclude" {
		whole.Add(whole, a.estimated)
	}
	if whole.Sign() != 0 {
		ratio, _ := new(big.Rat).Quo(a.estimated, whole).Float64()
		share = math.Round(ratio*1000) / 10
	}
	res.Estimates = EstimateInfo{Policy: policy, Total: decimalText(a.estimated), Text: FormatValue(m.Unit, a.estimated), SharePct: share}
	if compareFrom != "" {
		prior, e := s.aggregateMetric(ctx, p, b, m, request{from: compareFrom, to: compareTo, policy: policy, filters: in.Filters, where: where})
		if e != nil {
			return Result{}, e
		}
		before := folded(m, prior, compareFrom, compareTo)
		direction, good := judge(m, total.Cmp(before))
		cmp := &Comparison{From: compareFrom, To: compareTo, Total: rounded(m, before), Text: FormatValue(m.Unit, before), Direction: direction, Good: good}
		if before.Sign() != 0 {
			ratio, _ := new(big.Rat).Quo(new(big.Rat).Sub(total, before), new(big.Rat).Abs(before)).Float64()
			pct := math.Round(ratio*1000) / 10
			cmp.DeltaPct = &pct
		}
		res.Compare = cmp
	}
	if bucket != "" || in.Split != "" || policy == "split" {
		res.Series, res.Truncated = points(m, a, in.Split != "", maxSeriesRows)
		if res.Truncated {
			res.Message = "series truncated to " + strconv.Itoa(maxSeriesRows) + " rows; use a coarser bucket, a narrower range or a filter"
		}
	}
	if len(in.Facts) > 0 {
		if res.Facts, e = s.facts(ctx, p, b, m, in, req, res); e != nil {
			return Result{}, e
		}
	}
	return res, nil
}

// points orders the series by bucket then size, folding names past the largest into Other.
func points(m Metric, a aggregate, split bool, limit int) ([]Point, bool) {
	ranked := make([]string, 0, len(a.names))
	for name := range a.names {
		ranked = append(ranked, name)
	}
	sort.Slice(ranked, func(i, j int) bool {
		x, y := new(big.Rat).Abs(a.names[ranked[i]]), new(big.Rat).Abs(a.names[ranked[j]])
		if c := x.Cmp(y); c != 0 {
			return c > 0
		}
		return ranked[i] < ranked[j]
	})
	rank := map[string]int{}
	for i, name := range ranked {
		rank[name] = i
	}
	merged := map[cellKey]*big.Rat{}
	for key, v := range a.cells {
		if rank[key.name] >= maxSeriesName {
			key.name = "Other"
		}
		if merged[key] == nil {
			merged[key] = new(big.Rat)
		}
		merged[key].Add(merged[key], v)
	}
	keys := make([]cellKey, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	order := func(name string) int {
		if name == "Other" {
			return len(ranked)
		}
		return rank[name]
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].bucket != keys[j].bucket {
			return keys[i].bucket < keys[j].bucket
		}
		if keys[i].name != keys[j].name {
			return order(keys[i].name) < order(keys[j].name)
		}
		return !keys[i].estimated && keys[j].estimated
	})
	truncated := len(keys) > limit
	if truncated {
		keys = keys[:limit]
	}
	out := make([]Point, len(keys))
	for i, key := range keys {
		name := key.name
		if !split {
			name = m.Label
			if key.estimated {
				name = "Estimated"
			}
		}
		out[i] = Point{Bucket: key.bucket, Name: name, Value: decimalText(merged[key]), Text: FormatValue(m.Unit, merged[key]), Estimated: key.estimated}
	}
	return out, truncated
}

func formula(b *book, m Metric, policy string) string {
	var parts []string
	switch m.Kind {
	case "balance":
		parts = append(parts, "Holdings at the period end")
	case "rate":
		parts = append(parts, "Sum per month")
	default:
		parts = append(parts, "Sum over the period")
	}
	names := make([]string, 0, len(m.Accounts))
	for _, prefix := range m.Accounts {
		names = append(names, strings.TrimSuffix(prefix, ":"))
	}
	if len(names) > 0 {
		parts = append(parts, "of accounts under "+strings.Join(names, ", "))
	}
	if len(m.Exclude) > 0 {
		parts = append(parts, "leaving out "+strings.Join(m.Exclude, ", "))
	}
	if m.Unit == "eur" || m.Unit == "usd" {
		parts = append(parts, "valued in "+strings.ToUpper(m.Unit)+" at each entry's date")
	}
	if m.Sign == -1 {
		parts = append(parts, "with the sign flipped")
	}
	parts = append(parts, "estimates: "+policy)
	if m.Where != "" {
		parts = append(parts, "where "+m.Where)
	}
	return strings.Join(parts, ", ")
}

// LedgerCheck totals plain ledger accounts in the same unit and fold as the metric, so a test proves its filters and estimate policy.
func (s *Service) LedgerCheck(ctx context.Context, p principal.Principal, metric, from, to string, accounts, exclude []string) (Result, Result, error) {
	got, e := s.Query(ctx, p, QueryInput{Metric: metric, From: from, To: to})
	if e != nil {
		return Result{}, Result{}, e
	}
	ctx = reqcache.With(rates.WithQuoteCache(ctx))
	m, e := s.Metric(ctx, p, metric)
	if e != nil {
		return Result{}, Result{}, e
	}
	b, e := s.book(ctx, p)
	if e != nil {
		return Result{}, Result{}, e
	}
	plain := Metric{ID: m.ID, Label: m.Label, Unit: m.Unit, Kind: m.Kind, Additivity: m.Additivity, Estimates: "include", Accounts: accounts, Exclude: exclude, Sign: m.Sign}
	want, e := s.run(ctx, p, b, plain, QueryInput{From: from, To: to}, "")
	return got, want, e
}

// EntryRow is one posting behind a metric, for drill-through.
type EntryRow struct {
	Entry   string   `json:"entry"`
	Date    string   `json:"date"`
	Payee   string   `json:"payee"`
	Account string   `json:"account"`
	Value   string   `json:"value"`
	Text    string   `json:"text"`
	Tags    []string `json:"tags"`
}
type EntriesResult struct {
	Rows  []EntryRow `json:"rows"`
	Total int        `json:"total"`
	Ref   EntriesRef `json:"ref"`
}

// EntriesOf lists the postings a metric counted in a range, newest first, up to limit.
func (s *Service) EntriesOf(ctx context.Context, p principal.Principal, in QueryInput, where string, limit int) (EntriesResult, error) {
	if e := p.Require("ledger:read"); e != nil {
		return EntriesResult{}, e
	}
	ctx = reqcache.With(rates.WithQuoteCache(ctx))
	m, e := s.Metric(ctx, p, in.Metric)
	if e != nil {
		return EntriesResult{}, e
	}
	b, e := s.book(ctx, p)
	if e != nil {
		return EntriesResult{}, e
	}
	if e = checkFilters(b, in.Filters); e != nil {
		return EntriesResult{}, e
	}
	to := in.To
	if to == "" {
		to = time.Now().UTC().Format(day)
	}
	policy := in.Estimates
	if policy == "" {
		policy = m.Estimates
	}
	programs, e := s.compileWhere(m.Where, where)
	if e != nil {
		return EntriesResult{}, e
	}
	out := EntriesResult{Rows: []EntryRow{}, Ref: EntriesRef{From: in.From, To: to, Accounts: slices.Clone(m.Accounts), Exclude: slices.Clone(m.Exclude)}}
	for i := len(b.rows) - 1; i >= 0; i-- {
		r := &b.rows[i]
		if r.date > to || r.date < in.From || r.estimated && policy == "exclude" || len(m.Accounts) > 0 && !hasPrefixAny(r.account, m.Accounts) || hasPrefixAny(r.account, m.Exclude) || !b.passes(r, in.Filters) {
			continue
		}
		ok, e := s.passesWhere(ctx, p, b, r, programs)
		if e != nil {
			return EntriesResult{}, e
		}
		if !ok {
			continue
		}
		out.Total++
		if len(out.Rows) >= limit {
			continue
		}
		v, e := s.amountIn(ctx, p, b, r, m.Unit)
		if e != nil {
			return EntriesResult{}, e
		}
		v = new(big.Rat).Mul(v, big.NewRat(int64(m.Sign), 1))
		tags := r.tags
		if tags == nil {
			tags = []string{}
		}
		out.Rows = append(out.Rows, EntryRow{r.entry, r.date, r.name, b.accountLabel(r.acct, r.account), decimalText(v), FormatValue(m.Unit, v), tags})
	}
	return out, nil
}

// FieldValue is one real value of a dimension with how often it occurs.
type FieldValue struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Count int    `json:"count"`
	Total string `json:"total"`
}
type FieldValuesInput struct {
	Dimension string `json:"dimension"`
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
	Prefix    string `json:"prefix,omitempty" jsonschema:"keep values or labels starting with this text, case-insensitive"`
	Limit     int    `json:"limit,omitempty" jsonschema:"1..500, default 100"`
}

// FieldValues counts spending and income legs, so a transfer's two sides do not cancel to zero; account also counts asset legs.
func (s *Service) FieldValues(ctx context.Context, p principal.Principal, in FieldValuesInput) ([]FieldValue, error) {
	if e := p.Require("ledger:read"); e != nil {
		return nil, e
	}
	if in.Limit == 0 {
		in.Limit = 100
	}
	if in.Limit < 1 || in.Limit > 500 || len(in.Prefix) > 200 {
		return nil, fmt.Errorf("limit must be 1..500 and prefix at most 200 characters")
	}
	if in.From != "" && !validDate(in.From) || in.To != "" && !validDate(in.To) {
		return nil, fmt.Errorf("from and to must be YYYY-MM-DD")
	}
	ctx = reqcache.With(rates.WithQuoteCache(ctx))
	b, e := s.book(ctx, p)
	if e != nil {
		return nil, e
	}
	if e = b.checkField(in.Dimension); e != nil {
		return nil, e
	}
	type tally struct {
		count int
		total *big.Rat
	}
	seen := map[string]*tally{}
	for i := range b.rows {
		r := &b.rows[i]
		if in.From != "" && r.date < in.From || in.To != "" && r.date > in.To {
			continue
		}
		if in.Dimension != "account" && !strings.HasPrefix(r.account, "expenses:") && !strings.HasPrefix(r.account, "income:") {
			continue
		}
		for _, value := range b.values(r, in.Dimension) {
			if value == "" {
				continue
			}
			item := seen[value]
			if item == nil {
				item = &tally{total: new(big.Rat)}
				seen[value] = item
			}
			item.count++
			// An unquoted commodity still counts as an occurrence; only its total is unknown.
			if v, e := s.amountIn(ctx, p, b, r, "eur"); e == nil {
				item.total.Add(item.total, v)
			}
		}
	}
	prefix := strings.ToLower(in.Prefix)
	out := []FieldValue{}
	for value, item := range seen {
		label := b.display(in.Dimension, value)
		if prefix != "" && !strings.HasPrefix(strings.ToLower(value), prefix) && !strings.HasPrefix(strings.ToLower(label), prefix) {
			continue
		}
		out = append(out, FieldValue{value, label, item.count, decimalText(item.total)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Value < out[j].Value
	})
	if len(out) > in.Limit {
		out = out[:in.Limit]
	}
	return out, nil
}

// Annotations returns stored annotations overlapping the range, plus one span per tag matching rule ("trips" means trip:*).
func (s *Service) Annotations(ctx context.Context, p principal.Principal, rule, from, to string) ([]Annotation, error) {
	if e := p.Require("ledger:read"); e != nil {
		return nil, e
	}
	stored, e := list[Annotation](ctx, s.repo.db.Collection("annotations"), p.UserID, "id")
	if e != nil {
		return nil, e
	}
	ctx = reqcache.With(rates.WithQuoteCache(ctx))
	b, e := s.book(ctx, p)
	if e != nil {
		return nil, e
	}
	out := []Annotation{}
	overlaps := func(a Annotation) bool {
		end := a.To
		if end == "" {
			end = a.From
		}
		return (to == "" || a.From <= to) && (from == "" || end >= from)
	}
	for _, a := range stored {
		if a.TagRule != "" {
			if a.From == "" {
				a.From, a.To = b.tagSpan(a.TagRule)
			}
			if a.From == "" {
				continue
			}
		}
		if overlaps(a) {
			out = append(out, a)
		}
	}
	if rule == "trips" {
		rule = "trip:*"
	}
	if rule == "" {
		return out, nil
	}
	spans := map[string][2]string{}
	for i := range b.rows {
		for _, tag := range b.rows[i].tags {
			if !tagMatches(rule, tag) {
				continue
			}
			item, ok := spans[tag]
			if !ok || b.rows[i].date < item[0] {
				item[0] = b.rows[i].date
			}
			item[1] = max(item[1], b.rows[i].date)
			spans[tag] = item
		}
	}
	tags := make([]string, 0, len(spans))
	for tag := range spans {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	for _, tag := range tags {
		kind := "event"
		name := tag
		if rest, ok := strings.CutPrefix(tag, "trip:"); ok {
			kind, name = "trip", rest
		}
		a := Annotation{ID: tag, Label: b.label("tag", tag, humanize(name)), From: spans[tag][0], To: spans[tag][1], TagRule: tag, Kind: kind}
		if overlaps(a) {
			out = append(out, a)
		}
	}
	return out, nil
}

func tagMatches(rule, tag string) bool {
	if prefix, wild := strings.CutSuffix(rule, "*"); wild {
		return strings.HasPrefix(tag, prefix)
	}
	return rule == tag
}

func (b *book) tagSpan(rule string) (string, string) {
	from, to := "", ""
	for i := range b.rows {
		for _, tag := range b.rows[i].tags {
			if tagMatches(rule, tag) {
				if from == "" || b.rows[i].date < from {
					from = b.rows[i].date
				}
				to = max(to, b.rows[i].date)
			}
		}
	}
	return from, to
}
