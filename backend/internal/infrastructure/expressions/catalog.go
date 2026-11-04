package expressions

import (
	"sort"
	"strings"
)

type Function struct {
	Name        string `json:"name"`
	Signature   string `json:"signature"`
	Description string `json:"description"`
	Member      bool   `json:"member"`
}

// Functions describes the declarations in the same CEL environment used for evaluation.
func (e *Engine) Functions() []Function {
	docs := map[string]string{"abs": "Absolute value.", "round": "Round a number to the specified decimal places.", "pct": "Percentage of one number over another; zero denominator returns zero.", "sum": "Sum a list of numbers.", "min": "Smallest number in a list.", "max": "Largest number in a list.", "today": "Current date in Europe/Zagreb.", "now": "Current timestamp.", "month_of": "Extract the month from a date.", "day_of_month": "Extract the day from a date.", "days_in_month": "Number of days in a month.", "month": "Extract the month from a date."}
	docs["gtin_valid"] = "Check the GS1 checksum of an 8, 12, 13 or 14 digit GTIN; does not check registration."
	docs["iban_valid"] = "Check IBAN structure and MOD 97 checksum (15..34 characters); accepts ASCII spaces and lowercase. Does not check country-specific BBAN rules or account existence."
	docs["luhn"] = "Check the Luhn checksum of 2..1024 ASCII digits."
	docs["mod11_10"] = "Check the ISO 7064 MOD 11,10 checksum of 2..1024 ASCII digits; for Croatian OIB also require 11 digits."
	docs["mod97"] = "Check that 2..1024 ASCII digits have remainder 1 modulo 97, without integer overflow."
	out := []Function{}
	for name, declaration := range e.env.Functions() {
		if strings.HasPrefix(name, "_") || strings.HasPrefix(name, "@") {
			continue
		}
		for _, overload := range declaration.OverloadDecls() {
			args := []string{}
			for _, arg := range overload.ArgTypes() {
				args = append(args, arg.String())
			}
			description := docs[name]
			if description == "" {
				description = "CEL " + name + " function."
			}
			signature := name + "(" + strings.Join(args, ", ") + ") → " + overload.ResultType().String()
			out = append(out, Function{Name: name, Signature: signature, Description: description, Member: overload.IsMemberFunction()})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Signature < out[j].Signature
	})
	return out
}
