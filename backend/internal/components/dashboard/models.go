package dashboard

import (
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"time"
)

type Query struct {
	From   string            `json:"from"`
	Where  string            `json:"where,omitempty"`
	Derive map[string]string `json:"derive,omitempty"`
	Group  []string          `json:"group,omitempty"`
	Agg    map[string]string `json:"agg,omitempty"`
	Sort   []string          `json:"sort,omitempty"`
	Limit  int               `json:"limit,omitempty"`
	As     string            `json:"as,omitempty"`
	Key    string            `json:"key,omitempty"`
	Value  string            `json:"value,omitempty"`
	N      string            `json:"n,omitempty"`
	// HideZero drops result rows that have numeric value columns (the agg columns when aggregating, else every
	// numeric column) all equal to zero; rows without numeric value columns are kept.
	HideZero bool `json:"hide_zero,omitempty"`
}
type Column struct {
	Label  string `json:"label"`
	Value  string `json:"value" jsonschema:"CEL over row giving the cell"`
	Format string `json:"format,omitempty" jsonschema:"how the value prints"`
	Tone   string `json:"tone,omitempty" jsonschema:"CEL over row giving ok or neg"`
	Type   string `json:"type,omitempty" jsonschema:"data type; alignment follows it"`
	Total  bool   `json:"total,omitempty" jsonschema:"show a total row; only for additive money, number or count columns"`
}
type Axis struct {
	Field string `json:"field,omitempty"`
	Expr  string `json:"expr,omitempty"`
}
type Encoding struct {
	X     Axis  `json:"x"`
	Y     Axis  `json:"y"`
	Color *Axis `json:"color,omitempty"`
}
type Rule struct {
	Y     string `json:"y"`
	Label string `json:"label,omitempty"`
}

// XAxis says how a chart reads its x values; time gives a real time axis with zoom.
type XAxis struct {
	Type string `json:"type" jsonschema:"time or category"`
}

// ColorRule keeps a category's colour stable across charts, ranges and filters.
type ColorRule struct {
	By string `json:"by" jsonschema:"entity"`
}

// Drill says what a click on the panel opens.
type Drill struct {
	To     string `json:"to" jsonschema:"entries"`
	Filter string `json:"filter,omitempty" jsonschema:"same_as_page, or boolean CEL over row narrowing the entries"`
}

// Fixed opts a panel out of the page clock and shows a Fixed range label.
type Fixed struct {
	Reason string `json:"reason" jsonschema:"why this panel ignores the page's time range; shown to people"`
	Range  string `json:"range,omitempty" jsonschema:"time grammar for the panel's own range; default all"`
}
type Component struct {
	ID        string     `json:"id"`
	Type      string     `json:"type"`
	Title     string     `json:"title,omitempty"`
	Value     string     `json:"value,omitempty"`
	Delta     string     `json:"delta,omitempty"`
	Sparkline string     `json:"sparkline,omitempty"`
	Sub       string     `json:"sub,omitempty"`
	Tone      string     `json:"tone,omitempty"`
	Format    string     `json:"format,omitempty"`
	Rows      string     `json:"rows,omitempty"` // data source name, or a CEL expression returning a list of rows
	Columns   []Column   `json:"columns,omitempty"`
	Label     string     `json:"label,omitempty"`
	Actual    string     `json:"actual,omitempty"`
	Limit     string     `json:"limit,omitempty"`
	Mark      string     `json:"mark,omitempty"`
	Encoding  *Encoding  `json:"encoding,omitempty"`
	Rule      *Rule      `json:"rule,omitempty"`
	MD        string     `json:"md,omitempty"`
	Scenarios []string   `json:"scenarios,omitempty"`
	Metric    string     `json:"metric,omitempty" jsonschema:"metric id from metric_list on stat, time_chart, ranked_bars, sankey and entries; brings the unit, additivity and estimate policy"`
	Split     string     `json:"split,omitempty" jsonschema:"dimension to break the metric down by: category, payee, account, trip, tag, type, source, pot, class or an account group key"`
	X         *XAxis     `json:"x,omitempty" jsonschema:"x axis type on chart and time_chart"`
	Estimates string     `json:"estimates,omitempty" jsonschema:"overrides the metric's estimate policy"`
	Annotate  string     `json:"annotate,omitempty" jsonschema:"trips, or a tag rule such as trip:* or home-move"`
	Color     *ColorRule `json:"color,omitempty"`
	Drill     *Drill     `json:"drill,omitempty"`
	Compare   string     `json:"compare,omitempty" jsonschema:"overrides scope.compare; none states that no comparison makes sense"`
	Fixed     *Fixed     `json:"fixed,omitempty"`
}
type Placement struct {
	Span      int    `json:"span"`
	Component string `json:"component"`
}
type Row struct {
	Cols []Placement `json:"cols"`
}
type Page struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Layout []Row  `json:"layout"`
}

// ScopeTime is the page clock's default range and presets.
type ScopeTime struct {
	Default string   `json:"default" jsonschema:"time grammar, such as 12mo, ytd, 2026-Q2, since march or all"`
	Presets []string `json:"presets,omitempty" jsonschema:"time grammar choices offered beside the date field"`
}

// Scope gives the page one clock. Every CEL expression then reads params.from, params.to, params.compare_from and params.compare_to.
type Scope struct {
	Time    *ScopeTime `json:"time,omitempty"`
	Compare string     `json:"compare,omitempty" jsonschema:"previous_period, previous_year or none"`
}
type Explore struct {
	Dimensions []string `json:"dimensions,omitempty" jsonschema:"dimensions the page's filter bar offers"`
}
type Answer struct {
	Metric string   `json:"metric" jsonschema:"metric id the page's lead sentence is about"`
	Facts  []string `json:"facts,omitempty" jsonschema:"period_change, top_contributors, top_detractors, estimate_share, largest_entry"`
}

// LedgerRef names the plain ledger accounts a test must agree with.
type LedgerRef struct {
	Accounts []string `json:"accounts" jsonschema:"account id prefixes"`
	Exclude  []string `json:"exclude,omitempty" jsonschema:"account id prefixes left out"`
}

// SpecTest is a saved question: the metric must equal the ledger over the period.
type SpecTest struct {
	Ask          string    `json:"ask"`
	Metric       string    `json:"metric"`
	From         string    `json:"from" jsonschema:"YYYY-MM-DD"`
	To           string    `json:"to" jsonschema:"YYYY-MM-DD"`
	EqualsLedger LedgerRef `json:"equals_ledger"`
}
type Spec struct {
	ID         string            `json:"id"`
	Title      string            `json:"title"`
	Version    int               `json:"version"`
	Params     map[string]any    `json:"params,omitempty" jsonschema:"defaults CEL reads as params.*; with scope, params.from, to, compare_from and compare_to hold the page clock as YYYY-MM-DD"`
	Data       map[string]Query  `json:"data"`
	Computed   map[string]string `json:"computed,omitempty"`
	Pages      []Page            `json:"pages"`
	Components []Component       `json:"components"`
	Question   string            `json:"question,omitempty" jsonschema:"the question this dashboard answers"`
	Home       bool              `json:"home,omitempty" jsonschema:"true makes this the page the app opens on once published"`
	Scope      *Scope            `json:"scope,omitempty"`
	Explore    *Explore          `json:"explore,omitempty"`
	Answer     *Answer           `json:"answer,omitempty"`
	Tests      []SpecTest        `json:"tests,omitempty" jsonschema:"saved questions dashboard_test checks against the ledger; all must pass to publish"`
}
type Head struct {
	PublishedBy      principal.Actor `json:"published_by,omitempty" bson:"published_by,omitempty"`
	PublishedAt      *time.Time      `json:"published_at,omitempty" bson:"published_at,omitempty"`
	PublishNote      string          `json:"publish_note,omitempty" bson:"publish_note,omitempty"`
	Archived         bool            `json:"archived,omitempty" bson:"archived,omitempty"`
	Key              string          `json:"-" bson:"_id"`
	UserID           string          `json:"-" bson:"user_id"`
	ID               string          `json:"id" bson:"id"`
	Title            string          `json:"title" bson:"title"`
	PublishedVersion int             `json:"published_version" bson:"published_version"`
	DraftVersion     int             `json:"draft_version" bson:"draft_version"`
	Revision         int             `json:"revision" bson:"revision"`
	Home             bool            `json:"home,omitempty" bson:"home,omitempty"`
}
type Version struct {
	Key         string          `json:"-" bson:"_id"`
	UserID      string          `json:"-" bson:"user_id"`
	DashboardID string          `json:"dashboard_id" bson:"dashboard_id"`
	Version     int             `json:"version" bson:"version"`
	Spec        Spec            `json:"spec" bson:"spec"`
	Author      principal.Actor `json:"author" bson:"author"`
	CreatedAt   time.Time       `json:"created_at" bson:"created_at"`
	Note        string          `json:"note" bson:"note"`
}
type Issue struct {
	Path        string `json:"path"`
	Msg         string `json:"msg"`
	ComponentID string `json:"component_id,omitempty"`
	Field       string `json:"field,omitempty"`
	Expr        string `json:"expr,omitempty"`
	Hint        string `json:"hint,omitempty"`
}

func (i Issue) String() string {
	s := i.Path + ": " + i.Msg
	if i.Hint != "" {
		s += " (" + i.Hint + ")"
	}
	return s
}

type Validation struct {
	OK       bool    `json:"ok"`
	Errors   []Issue `json:"errors"`
	Warnings []Issue `json:"warnings"`
	Cost     uint64  `json:"cost"`
}
type Rendered struct {
	Pages      []Page         `json:"pages,omitempty" bson:"pages,omitempty"`
	HTMLURL    string         `json:"html_url,omitempty"`
	PNGURL     string         `json:"png_url,omitempty"`
	HTML       string         `json:"html"`
	Values     map[string]any `json:"values"`
	Computed   map[string]any `json:"computed,omitempty"`
	Validation Validation     `json:"validation"`
	// Resolved holds each component after its templates ran, for lint to read the text people actually see.
	Resolved map[string]Component `json:"-" bson:"-"`
}
