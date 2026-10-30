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
	Value  string `json:"value"`
	Format string `json:"format,omitempty"`
	Tone   string `json:"tone,omitempty"`
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
type Component struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Title     string    `json:"title,omitempty"`
	Value     string    `json:"value,omitempty"`
	Delta     string    `json:"delta,omitempty"`
	Sub       string    `json:"sub,omitempty"`
	Tone      string    `json:"tone,omitempty"`
	Format    string    `json:"format,omitempty"`
	Rows      string    `json:"rows,omitempty"` // data source name, or a CEL expression returning a list of rows
	Columns   []Column  `json:"columns,omitempty"`
	Label     string    `json:"label,omitempty"`
	Actual    string    `json:"actual,omitempty"`
	Limit     string    `json:"limit,omitempty"`
	Mark      string    `json:"mark,omitempty"`
	Encoding  *Encoding `json:"encoding,omitempty"`
	Rule      *Rule     `json:"rule,omitempty"`
	MD        string    `json:"md,omitempty"`
	Scenarios []string  `json:"scenarios,omitempty"`
	Metric    string    `json:"metric,omitempty"`
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
type Spec struct {
	ID         string            `json:"id"`
	Title      string            `json:"title"`
	Version    int               `json:"version"`
	Params     map[string]any    `json:"params,omitempty"`
	Data       map[string]Query  `json:"data"`
	Computed   map[string]string `json:"computed,omitempty"`
	Pages      []Page            `json:"pages"`
	Components []Component       `json:"components"`
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
}
