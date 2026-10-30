package semantic

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// TimeGrammar documents ParseRange for the catalog; every example here is covered by its test.
var TimeGrammar = []string{"30d", "12w", "12mo", "2y", "ytd", "qtd", "mtd", "today", "all", "this month", "last month", "this quarter", "last quarter", "this year", "last year", "2026", "2026-Q2", "2026-03", "2026-03-14", "march", "since march", "since 2026-03", "aug to oct", "2026-01 to 2026-03", "2026-01-15..2026-02-20"}

var (
	relativeRange = regexp.MustCompile(`^(\d{1,4})(d|w|mo|y)$`)
	quarterRange  = regexp.MustCompile(`^(\d{4})-q([1-4])$`)
	yearRange     = regexp.MustCompile(`^\d{4}$`)
	monthRange    = regexp.MustCompile(`^\d{4}-\d{2}$`)
)

var monthNames = map[string]time.Month{"jan": 1, "january": 1, "feb": 2, "february": 2, "mar": 3, "march": 3, "apr": 4, "april": 4, "may": 5, "jun": 6, "june": 6, "jul": 7, "july": 7, "aug": 8, "august": 8, "sep": 9, "sept": 9, "september": 9, "oct": 10, "october": 10, "nov": 11, "november": 11, "dec": 12, "december": 12}

const day = "2006-01-02"

func monthStart(y int, m time.Month) time.Time { return time.Date(y, m, 1, 0, 0, 0, 0, time.UTC) }

// span reads one point in time as a from..to span; a bare month name means its latest occurrence.
func span(text string, today time.Time) (time.Time, time.Time, bool) {
	switch {
	case yearRange.MatchString(text):
		y, _ := strconv.Atoi(text)
		return time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(y, 12, 31, 0, 0, 0, 0, time.UTC), true
	case monthRange.MatchString(text):
		t, e := time.Parse("2006-01", text)
		return t, t.AddDate(0, 1, -1), e == nil
	case quarterRange.MatchString(text):
		m := quarterRange.FindStringSubmatch(text)
		y, _ := strconv.Atoi(m[1])
		q, _ := strconv.Atoi(m[2])
		start := monthStart(y, time.Month(3*q-2))
		return start, start.AddDate(0, 3, -1), true
	}
	if t, e := time.Parse(day, text); e == nil {
		return t, t, true
	}
	if m, ok := monthNames[text]; ok {
		start := monthStart(today.Year(), m)
		if start.After(today) {
			start = start.AddDate(-1, 0, 0)
		}
		return start, start.AddDate(0, 1, -1), true
	}
	return time.Time{}, time.Time{}, false
}

// ParseRange reads the time grammar into inclusive YYYY-MM-DD bounds; "all" has an empty from.
func ParseRange(text string, now time.Time) (string, string, error) {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	text = strings.ToLower(strings.Join(strings.Fields(text), " "))
	result := func(from, to time.Time) (string, string, error) {
		if from.After(to) {
			return "", "", fmt.Errorf("time range %q starts after it ends", text)
		}
		return from.Format(day), to.Format(day), nil
	}
	quarter := monthStart(today.Year(), time.Month(3*((int(today.Month())-1)/3)+1))
	switch text {
	case "all":
		return "", today.Format(day), nil
	case "today":
		return result(today, today)
	case "ytd", "this year":
		return result(monthStart(today.Year(), 1), today)
	case "qtd", "this quarter":
		return result(quarter, today)
	case "mtd", "this month":
		return result(monthStart(today.Year(), today.Month()), today)
	case "last month":
		start := monthStart(today.Year(), today.Month()).AddDate(0, -1, 0)
		return result(start, start.AddDate(0, 1, -1))
	case "last quarter":
		return result(quarter.AddDate(0, -3, 0), quarter.AddDate(0, 0, -1))
	case "last year":
		return result(monthStart(today.Year()-1, 1), monthStart(today.Year(), 1).AddDate(0, 0, -1))
	}
	if m := relativeRange.FindStringSubmatch(text); m != nil {
		n, _ := strconv.Atoi(m[1])
		if n == 0 {
			return "", "", fmt.Errorf("time range %q must be longer than zero", text)
		}
		// N units ending today, inclusive: 30d is exactly 30 days and 12mo starts the day after the same date a year ago.
		start := today.AddDate(0, 0, 1-n)
		switch m[2] {
		case "w":
			start = today.AddDate(0, 0, 1-7*n)
		case "mo":
			start = today.AddDate(0, -n, 1)
		case "y":
			start = today.AddDate(-n, 0, 1)
		}
		return result(start, today)
	}
	if rest, ok := strings.CutPrefix(text, "since "); ok {
		from, _, ok := span(rest, today)
		if !ok {
			return "", "", grammarError(text)
		}
		return result(from, today)
	}
	for _, sep := range []string{" to ", ".."} {
		if a, b, ok := strings.Cut(text, sep); ok {
			from, _, ok1 := span(strings.TrimSpace(a), today)
			_, to, ok2 := span(strings.TrimSpace(b), today)
			if !ok1 || !ok2 {
				return "", "", grammarError(text)
			}
			// "nov to feb" crosses a year end, so the start moves back rather than failing.
			if _, named := monthNames[strings.TrimSpace(a)]; named && from.After(to) {
				from = from.AddDate(-1, 0, 0)
			}
			return result(from, to)
		}
	}
	if from, to, ok := span(text, today); ok {
		return result(from, to)
	}
	return "", "", grammarError(text)
}

func grammarError(text string) error {
	return fmt.Errorf("time range %q is not understood; use forms such as %s", text, strings.Join(TimeGrammar[:12], ", "))
}

// ComparePeriod returns the period to compare from..to with; an empty result means there is nothing to compare.
func ComparePeriod(compare, from, to string) (string, string, error) {
	if e := oneOf("compare", compare, "", "none", "previous_period", "previous_year"); e != nil {
		return "", "", e
	}
	if compare == "" || compare == "none" || from == "" {
		return "", "", nil
	}
	a, e := time.Parse(day, from)
	if e != nil {
		return "", "", fmt.Errorf("from must be YYYY-MM-DD")
	}
	b, e := time.Parse(day, to)
	if e != nil {
		return "", "", fmt.Errorf("to must be YYYY-MM-DD")
	}
	if compare == "previous_year" {
		return a.AddDate(-1, 0, 0).Format(day), b.AddDate(-1, 0, 0).Format(day), nil
	}
	end := a.AddDate(0, 0, -1)
	return end.AddDate(0, 0, -int(b.Sub(a).Hours()/24)).Format(day), end.Format(day), nil
}
