package ledger

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Decimal preserves exact base-ten values in MongoDB and strings in public JSON.
type Decimal struct{ bson.Decimal128 }

var decimalPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?$`)

func ParseDecimal(s string) (Decimal, error) {
	if len(strings.TrimPrefix(s, "-")) > 80 || !decimalPattern.MatchString(s) {
		return Decimal{}, fmt.Errorf("amount must be a finite decimal string")
	}
	d, e := bson.ParseDecimal128(s)
	if e != nil {
		return Decimal{}, fmt.Errorf("amount exceeds Decimal128 precision")
	}
	return Decimal{d}, nil
}
func MustDecimal(s string) Decimal {
	d, e := ParseDecimal(s)
	if e != nil {
		panic(e)
	}
	return d
}
func (d Decimal) String() string {
	raw := d.Decimal128.String()
	if !strings.ContainsAny(raw, "eE") {
		return raw
	}
	r, ok := new(big.Rat).SetString(raw)
	if !ok {
		return raw
	}
	s, e := exactString(r)
	if e != nil {
		return raw
	}
	return s
}
func exactString(r *big.Rat) (string, error) {
	d := new(big.Int).Set(r.Denom())
	two, five := big.NewInt(2), big.NewInt(5)
	a, b := 0, 0
	rem := new(big.Int)
	for rem.Mod(d, two).Sign() == 0 {
		d.Quo(d, two)
		a++
	}
	for rem.Mod(d, five).Sign() == 0 {
		d.Quo(d, five)
		b++
	}
	if d.Cmp(big.NewInt(1)) != 0 {
		return "", fmt.Errorf("amount is not a finite decimal")
	}
	s := r.FloatString(max(a, b))
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s, nil
}
func DecimalFromRat(r *big.Rat) (Decimal, error) {
	s, e := exactString(r)
	if e != nil {
		return Decimal{}, e
	}
	return ParseDecimal(s)
}
func (d Decimal) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }
func (d *Decimal) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("amount must be a decimal string")
	}
	v, err := ParseDecimal(s)
	if err == nil {
		*d = v
	}
	return err
}
func (d Decimal) MarshalBSONValue() (byte, []byte, error) {
	t, b, e := bson.MarshalValue(d.Decimal128)
	return byte(t), b, e
}
func (d *Decimal) UnmarshalBSONValue(t byte, b []byte) error {
	return bson.UnmarshalValue(bson.Type(t), b, &d.Decimal128)
}
func (d Decimal) Rat() *big.Rat {
	r, ok := new(big.Rat).SetString(d.String())
	if !ok {
		return new(big.Rat)
	}
	return r
}
func (d Decimal) Float() float64 {
	if !d.IsNaN() && d.IsInf() == 0 {
		v, err := strconv.ParseFloat(d.Decimal128.String(), 64)
		if v != 0 && (err == nil || errors.Is(err, strconv.ErrRange)) {
			return v
		}
	}
	v, _ := d.Rat().Float64()
	return v
}
func (d Decimal) Negate() Decimal {
	h, l := d.Decimal128.GetBytes()
	return Decimal{bson.NewDecimal128(h^(uint64(1)<<63), l)}
}

type Amount struct {
	Value     Decimal `json:"v" bson:"v"`
	Commodity string  `json:"c" bson:"c"`
}
type Account struct {
	UserID      string            `json:"-" bson:"user_id"`
	Key         string            `json:"-" bson:"_id"`
	ID          string            `json:"id" bson:"id"`
	Type        string            `json:"type" bson:"type"`
	Name        string            `json:"name" bson:"name"`
	Pot         string            `json:"pot,omitempty" bson:"pot,omitempty"`
	Commodities []string          `json:"commodities" bson:"commodities"`
	Location    string            `json:"location,omitempty" bson:"location,omitempty"`
	Class       string            `json:"class,omitempty" bson:"class,omitempty"`
	Groups      map[string]string `json:"groups,omitempty" bson:"groups,omitempty"`
	ClosedAt    *time.Time        `json:"closed_at,omitempty" bson:"closed_at,omitempty"`
	DisplayName string            `json:"display_name,omitempty" bson:"display_name,omitempty" jsonschema:"human name shown everywhere; name keeps working when this is empty"`
	Note        string            `json:"note,omitempty" bson:"note,omitempty" jsonschema:"working notes, shown in Details rather than beside the name"`
	Estimated   *bool             `json:"estimated,omitempty" bson:"estimated,omitempty" jsonschema:"true marks reconstructed or modeled amounts; defaults from the expenses:estimated id prefix"`
}

// IsEstimated is the stored flag, or for accounts older than the flag, the expenses:estimated id prefix.
func (a Account) IsEstimated() bool {
	if a.Estimated != nil {
		return *a.Estimated
	}
	return a.ID == "expenses:estimated" || strings.HasPrefix(a.ID, "expenses:estimated:")
}

// Label is the name people read.
func (a Account) Label() string {
	if a.DisplayName != "" {
		return a.DisplayName
	}
	return a.Name
}

type Posting struct {
	Account string  `json:"account" bson:"account"`
	Amount  Amount  `json:"amount" bson:"amount"`
	Cost    *Amount `json:"cost,omitempty" bson:"cost,omitempty"`
	Price   *Amount `json:"price,omitempty" bson:"price,omitempty"`
	Memo    string  `json:"memo,omitempty" bson:"memo,omitempty"`
}
type Link struct {
	Rel string `json:"rel" bson:"rel"`
	ID  string `json:"id" bson:"id"`
}
type Entry struct {
	UserID    string          `json:"-" bson:"user_id"`
	ID        string          `json:"id" bson:"_id"`
	Seq       int64           `json:"seq" bson:"seq"`
	Date      string          `json:"date" bson:"date"`
	Kind      string          `json:"kind" bson:"kind"`
	Payee     string          `json:"payee" bson:"payee"`
	Postings  []Posting       `json:"postings" bson:"postings"`
	Tags      []string        `json:"tags" bson:"tags"`
	Links     []Link          `json:"links" bson:"links"`
	Status    string          `json:"status" bson:"status"`
	Actor     principal.Actor `json:"actor" bson:"actor"`
	CreatedAt time.Time       `json:"created_at" bson:"created_at"`
}
type EntryInput struct {
	ID       string    `json:"id,omitempty" bson:"-"`
	Date     string    `json:"date,omitempty"`
	Payee    string    `json:"payee,omitempty"`
	Postings []Posting `json:"postings,omitempty"`
	Tags     []string  `json:"tags,omitempty"`
	Commit   bool      `json:"commit,omitempty"`
}
type EntryResult struct {
	Entry    Entry  `json:"entry"`
	Balanced bool   `json:"balanced"`
	EventID  string `json:"event_id"`
}
type EntriesInput struct {
	From     string   `json:"from,omitempty"`
	To       string   `json:"to,omitempty"`
	Account  string   `json:"account,omitempty"`
	Accounts []string `json:"accounts,omitempty" jsonschema:"match postings in any of these accounts; cannot be combined with account"`
	Kind     string   `json:"kind,omitempty"`
	Tag      string   `json:"tag,omitempty"`
	Text     string   `json:"text,omitempty"`
	Status   string   `json:"status,omitempty"`
	// Effective hides reversed originals and their reversals, leaving the latest replacement of each chain.
	Effective    bool   `json:"effective,omitempty"`
	Cursor       int64  `json:"cursor,omitempty"`
	CursorDate   string `json:"cursor_date,omitempty" jsonschema:"date returned with next_cursor when sorting by date"`
	CursorAmount string `json:"cursor_amount,omitempty" jsonschema:"amount returned with next_cursor when sorting by amount"`
	Sort         string `json:"sort,omitempty" jsonschema:"date, -date, amount or -amount; omitted sorts by descending sequence"`
	Limit        int    `json:"limit,omitempty"`
}
type EntriesPage struct {
	Items            []Entry `json:"items"`
	NextCursor       int64   `json:"next_cursor,omitempty"`
	NextCursorDate   string  `json:"next_cursor_date,omitempty"`
	NextCursorAmount string  `json:"next_cursor_amount,omitempty"`
}
type AccountsInput struct {
	Type          string `json:"type,omitempty"`
	Pot           string `json:"pot,omitempty"`
	IncludeClosed bool   `json:"include_closed,omitempty"`
	Cursor        string `json:"cursor,omitempty"`
	Limit         int    `json:"limit,omitempty"`
}
type AccountsPage struct {
	Items      []Account `json:"items"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

var accountClasses = []string{"bank", "cash", "metal_holding", "receivable", "liability", "equity", "income", "expense"}
var groupKey = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)

// ValidateGroupKey checks a group dimension name; it is also used as a Mongo field name.
func ValidateGroupKey(k string) bool { return groupKey.MatchString(k) }

func validateClassGroups(class string, groups map[string]string) error {
	if class != "" && !slices.Contains(accountClasses, class) {
		return fmt.Errorf("class must be one of %s", strings.Join(accountClasses, ", "))
	}
	if len(groups) > 20 {
		return fmt.Errorf("at most 20 groups")
	}
	for k, v := range groups {
		if !groupKey.MatchString(k) || strings.TrimSpace(v) == "" || len(v) > 100 {
			return fmt.Errorf("group dimensions are 1..40 letters, digits, _ or -, with values of 1..100 characters")
		}
	}
	return nil
}
func Validate(in EntryInput) error {
	if _, err := time.Parse("2006-01-02", in.Date); err != nil {
		return fmt.Errorf("date must be YYYY-MM-DD")
	}
	if len(in.Payee) > 300 || len(in.Postings) < 2 || len(in.Postings) > 100 || len(in.Tags) > 50 {
		return fmt.Errorf("entry needs 2..100 postings, at most 50 tags, payee at most 300 characters")
	}
	for _, tag := range in.Tags {
		if strings.TrimSpace(tag) == "" || len(tag) > 100 {
			return fmt.Errorf("tags must contain 1..100 characters")
		}
	}
	sums := map[string]*big.Rat{}
	for _, p := range in.Postings {
		if p.Account == "" || p.Amount.Commodity == "" || len(p.Account) > 150 || len(p.Amount.Commodity) > 20 || len(p.Memo) > 1000 {
			return fmt.Errorf("posting account and commodity are required")
		}
		if _, err := ParseDecimal(p.Amount.Value.String()); err != nil {
			return err
		}
		r := sums[p.Amount.Commodity]
		if r == nil {
			r = new(big.Rat)
			sums[p.Amount.Commodity] = r
		}
		r.Add(r, p.Amount.Value.Rat())
	}
	for c, s := range sums {
		if s.Sign() != 0 {
			return fmt.Errorf("postings do not balance for %s", c)
		}
	}
	return nil
}
