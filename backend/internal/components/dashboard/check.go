package dashboard

import (
	"strings"
	"unicode/utf16"

	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
)

// CheckExpression applies the same aggregate grammar used by dashboard evaluation.
func CheckExpression(engine *expressions.Engine, expr, role string) expressions.CheckResult {
	if role != "agg" {
		return engine.Check(expr)
	}
	op, args, err := parseAgg(expr)
	if err != nil {
		return expressions.CheckResult{Issues: []expressions.Issue{{Msg: err.Error(), End: len(utf16.Encode([]rune(expr)))}}}
	}
	result := expressions.CheckResult{OK: true, Type: "double", Issues: []expressions.Issue{}}
	if op == "count" {
		return result
	}
	if op == "last" {
		result.Type = "dyn"
	}
	searchFrom := 0
	for _, arg := range args {
		offset := strings.Index(expr[searchFrom:], arg) + searchFrom
		checked := engine.Check(arg)
		if op == "last" && checked.OK {
			result.Type = checked.Type
		}
		for _, issue := range checked.Issues {
			shift := len(utf16.Encode([]rune(expr[:offset])))
			issue.Start += shift
			issue.End += shift
			result.Issues = append(result.Issues, issue)
		}
		if !checked.OK {
			result.OK = false
		}
		searchFrom = offset + len(arg)
	}
	return result
}
