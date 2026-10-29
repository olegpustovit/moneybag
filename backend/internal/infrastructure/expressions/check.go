package expressions

import (
	"unicode/utf16"

	"cel.dev/cel-go/common"
)

type Issue struct {
	Msg   string `json:"msg"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}
type CheckResult struct {
	OK     bool    `json:"ok"`
	Type   string  `json:"type"`
	Issues []Issue `json:"issues"`
}

// Check compiles without evaluating. Offsets use UTF-16 code units for browser editors.
func (e *Engine) Check(src string) CheckResult {
	result := CheckResult{Issues: []Issue{}}
	if len(src) > 2000 {
		result.Issues = append(result.Issues, Issue{Msg: "expression exceeds 2000 characters"})
		return result
	}
	ast, issues := e.env.Compile(src)
	if issues.Err() != nil {
		source := common.NewTextSource(src)
		chars := []rune(src)
		for _, issue := range issues.Errors() {
			offset, ok := source.LocationOffset(issue.Location)
			if !ok || offset < 0 {
				offset = 0
			}
			if int(offset) > len(chars) {
				offset = int32(len(chars))
			}
			start := len(utf16.Encode(chars[:offset]))
			end := start
			if int(offset) < len(chars) {
				end += len(utf16.Encode(chars[offset : offset+1]))
			}
			result.Issues = append(result.Issues, Issue{Msg: issue.Message, Start: start, End: end})
		}
		return result
	}
	result.OK = true
	result.Type = ast.OutputType().String()
	return result
}
