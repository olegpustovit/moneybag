package imports

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/xuri/excelize/v2"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type Mapping struct {
	DateFormat string `json:"date_format,omitempty"`
	Date       string `json:"date,omitempty"`
	Amount     string `json:"amount,omitempty"`
	Currency   string `json:"currency,omitempty"`
	Payee      string `json:"payee,omitempty"`
	ID         string `json:"id,omitempty"`
	Balance    string `json:"balance,omitempty"`
	Category   string `json:"category,omitempty"`
}
type Parsed struct {
	Date, Payee, ExternalID, Currency, Category string
	Amount                                      ledger.Decimal
	Balance                                     *ledger.Decimal
	Raw                                         map[string]string
	DedupeKey                                   string
}

func header(s string) string {
	return strings.ToLower(strings.TrimSpace(strings.TrimPrefix(s, "\ufeff")))
}

var aliases = map[string][]string{
	"date":     {"date", "date time", "completed date", "created date", "transaction date", "дата", "дата операції", "дата i час операцiї", "дата і час операції"},
	"amount":   {"amount", "amount (transaction currency)", "net amount", "сума", "сума операції", "сума у валюті картки", "сума в валюті картки"},
	"currency": {"currency", "валюта", "валюта операції", "валюта картки"},
	"payee":    {"description", "payee", "merchant", "payment reference", "details", "опис", "опис операції", "деталі операції", "призначення платежу"},
	"id":       {"id", "transferwise id", "transaction id", "reference number", "ідентифікатор"},
	"balance":  {"running balance", "balance", "closing balance", "залишок", "залишок після операції"},
	"category": {"category", "account", "категорія"},
}

func parseDate(s string) (string, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"2006-01-02", "2006-01-02 15:04:05", time.RFC3339, "02.01.2006", "02.01.2006 15:04:05", "02.01.2006 15:04", "02-01-2006", "02/01/2006", "01/02/2006", "2006/01/02", "02 Jan 2006"} {
		if d, e := time.Parse(layout, s); e == nil {
			return d.Format("2006-01-02"), nil
		}
	}
	if serial, e := strconv.ParseFloat(s, 64); e == nil && serial > 20000 && serial < 100000 {
		if d, e := excelize.ExcelDateToTime(serial, false); e == nil {
			return d.Format("2006-01-02"), nil
		}
	}
	return "", fmt.Errorf("unrecognized date %q", s)
}
func parseAmount(s string) (ledger.Decimal, error) {
	s = strings.TrimSpace(s)
	negative := strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")")
	s = strings.Trim(s, "()")
	s = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '\u00a0' {
			return -1
		}
		if r == '−' {
			return '-'
		}
		return r
	}, s)
	if strings.Contains(s, ",") && strings.Contains(s, ".") {
		if strings.LastIndex(s, ",") > strings.LastIndex(s, ".") {
			s = strings.ReplaceAll(s, ".", "")
			s = strings.ReplaceAll(s, ",", ".")
		} else {
			s = strings.ReplaceAll(s, ",", "")
		}
	} else if strings.Contains(s, ",") {
		s = strings.ReplaceAll(s, ",", ".")
	}
	s = strings.TrimPrefix(s, "+")
	if negative && !strings.HasPrefix(s, "-") {
		s = "-" + s
	}
	return ledger.ParseDecimal(s)
}

var parseSlots = make(chan struct{}, 2)

func Parse(content []byte, format string, mapping Mapping) ([]Parsed, error) {
	select {
	case parseSlots <- struct{}{}:
		defer func() { <-parseSlots }()
	default:
		return nil, fmt.Errorf("two statements are already parsing; retry shortly")
	}
	if mapping.DateFormat != "" && mapping.DateFormat != "iso" && mapping.DateFormat != "dmy" && mapping.DateFormat != "mdy" {
		return nil, fmt.Errorf("mapping.date_format must be iso, dmy or mdy")
	}
	if len(content) > 8*1024*1024 {
		return nil, fmt.Errorf("statement exceeds 8 MiB")
	}
	var records [][]string
	switch format {
	case "privatbank_xlsx":
		book, e := excelize.OpenReader(bytes.NewReader(content), excelize.Options{UnzipSizeLimit: 64 * 1024 * 1024, UnzipXMLSizeLimit: 8 * 1024 * 1024})
		if e != nil {
			return nil, fmt.Errorf("invalid XLSX workbook")
		}
		defer book.Close()
		sheets := book.GetSheetList()
		if len(sheets) == 0 {
			return nil, fmt.Errorf("workbook has no worksheets")
		}
		records, e = book.GetRows(sheets[0])
		if e != nil {
			return nil, fmt.Errorf("cannot read statement worksheet")
		}
	case "plaid":
		var e error
		records, e = plaidRecords(content)
		if e != nil {
			return nil, e
		}
	case "wise_csv", "generic_csv":
		if !utf8.Valid(content) {
			return nil, fmt.Errorf("CSV must be UTF-8")
		}
		reader := csv.NewReader(bytes.NewReader(content))
		reader.FieldsPerRecord = -1
		if line, _, _ := bytes.Cut(content, []byte("\n")); bytes.Count(line, []byte(";")) > bytes.Count(line, []byte(",")) {
			reader.Comma = ';'
		}
		for {
			row, err := reader.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("malformed CSV")
			}
			records = append(records, row)
			if len(records) > 10020 {
				return nil, fmt.Errorf("statement exceeds 10000 rows")
			}
		}
	default:
		return nil, fmt.Errorf("format must be privatbank_xlsx, wise_csv, generic_csv or plaid JSON")
	}
	if len(records) < 2 {
		return nil, fmt.Errorf("statement needs a header and transaction rows")
	}
	if len(records) > 10020 {
		return nil, fmt.Errorf("statement exceeds 10000 rows")
	}
	var indices map[string]int
	headerRow := -1
	explicit := map[string]string{"date": mapping.Date, "amount": mapping.Amount, "currency": mapping.Currency, "payee": mapping.Payee, "id": mapping.ID, "balance": mapping.Balance, "category": mapping.Category}
	for i, row := range records[:min(len(records), 20)] {
		found := map[string]int{}
		for j, value := range row {
			h := header(value)
			for field, names := range aliases {
				if wanted := explicit[field]; wanted != "" {
					if h == header(wanted) {
						found[field] = j
					}
					continue
				}
				for _, name := range names {
					if h == name {
						found[field] = j
						break
					}
				}
			}
		}
		if _, ok := found["date"]; ok {
			if _, ok := found["amount"]; ok {
				headerRow = i
				indices = found
				break
			}
		}
	}
	if headerRow < 0 {
		return nil, fmt.Errorf("date and amount columns not found; supply mapping with exact header names")
	}
	if _, ok := indices["currency"]; !ok {
		return nil, fmt.Errorf("currency column not found; supply mapping")
	}
	out := []Parsed{}
	for i, row := range records[headerRow+1:] {
		empty := true
		for _, v := range row {
			if strings.TrimSpace(v) != "" {
				empty = false
				break
			}
		}
		if empty {
			continue
		}
		get := func(field string) string {
			j, ok := indices[field]
			if !ok || j >= len(row) {
				return ""
			}
			return strings.TrimSpace(row[j])
		}
		date, e := statementDate(get("date"), mapping.DateFormat)
		if e != nil {
			return nil, fmt.Errorf("row %d: %w", i+headerRow+2, e)
		}
		amount, e := parseAmount(get("amount"))
		if e != nil {
			return nil, fmt.Errorf("row %d: invalid amount", i+headerRow+2)
		}
		currency := strings.TrimSpace(get("currency"))
		if currency == "" {
			return nil, fmt.Errorf("row %d: currency required", i+headerRow+2)
		}
		payee := get("payee")
		if payee == "" {
			payee = "Bank transaction"
		}
		raw := map[string]string{}
		for j, name := range records[headerRow] {
			if j < len(row) {
				raw[name] = row[j]
			}
		}
		p := Parsed{Date: date, Payee: payee, ExternalID: get("id"), Currency: currency, Category: get("category"), Amount: amount, Raw: raw}
		if value := get("balance"); value != "" {
			balance, e := parseAmount(value)
			if e != nil {
				return nil, fmt.Errorf("row %d: invalid balance", i+headerRow+2)
			}
			p.Balance = &balance
		} // shortcut: exports without bank IDs can flag identical same-day payments; map bank IDs to distinguish them.
		dedupe := p.ExternalID
		if dedupe == "" {
			dedupe = p.Date + "\x00" + p.Amount.Rat().RatString() + "\x00" + p.Currency + "\x00" + strings.ToLower(p.Payee)
		}
		h := sha256.Sum256([]byte(dedupe))
		p.DedupeKey = hex.EncodeToString(h[:])
		out = append(out, p)
	}
	if len(out) > 10000 {
		return nil, fmt.Errorf("statement exceeds 10000 rows")
	}
	return out, nil
}

func statementDate(raw, format string) (string, error) {
	raw = strings.TrimSpace(raw)
	if format != "" {
		for _, layout := range []string{"2006-01-02", time.RFC3339, "2006-01-02 15:04:05"} {
			if d, e := time.Parse(layout, raw); e == nil {
				return d.Format("2006-01-02"), nil
			}
		}
		if format == "iso" {
			return "", fmt.Errorf("date must use ISO YYYY-MM-DD")
		}
		layouts := []string{"2/1/2006", "2.1.2006", "2-1-2006"}
		if format == "mdy" {
			layouts = []string{"1/2/2006", "1.2.2006", "1-2-2006"}
		}
		for _, layout := range layouts {
			if d, e := time.Parse(layout, raw); e == nil {
				return d.Format("2006-01-02"), nil
			}
		}
		if strings.ContainsAny(raw, "/.-") {
			return "", fmt.Errorf("date does not match mapping.date_format")
		}
		return parseDate(raw)
	}
	parts := strings.Split(raw, "/")
	if len(parts) == 3 {
		a, ea := strconv.Atoi(parts[0])
		b, eb := strconv.Atoi(parts[1])
		if ea == nil && eb == nil && len(parts[2]) == 4 && a > 0 && b > 0 && a <= 12 && b <= 12 && a != b {
			return "", fmt.Errorf("ambiguous slash date; set mapping.date_format to dmy or mdy")
		}
	}
	return parseDate(raw)
}
