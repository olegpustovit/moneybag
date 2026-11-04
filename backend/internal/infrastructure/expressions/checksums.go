package expressions

import (
	"strings"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
)

func checksumOptions() []cel.EnvOption {
	options := []cel.EnvOption{}
	for name, check := range map[string]func(string) bool{
		"gtin_valid": gtinValid, "iban_valid": ibanValid, "luhn": luhnValid,
		"mod11_10": mod1110Valid, "mod97": mod97Valid,
	} {
		options = append(options, cel.Function(name, cel.Overload("mb_"+name,
			[]*cel.Type{cel.StringType}, cel.BoolType, cel.UnaryBinding(func(v ref.Val) ref.Val {
				return types.Bool(check(string(v.(types.String))))
			}))))
	}
	return options
}

func digits(s string) bool {
	if len(s) < 2 || len(s) > 1024 {
		return false
	}
	for i := range s {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func gtinValid(s string) bool {
	if len(s) != 8 && len(s) != 12 && len(s) != 13 && len(s) != 14 {
		return false
	}
	if !digits(s) {
		return false
	}
	sum := 0
	for i := len(s) - 1; i >= 0; i-- {
		weight := 1
		if (len(s)-1-i)%2 == 1 {
			weight = 3
		}
		sum += int(s[i]-'0') * weight
	}
	return sum%10 == 0
}

func luhnValid(s string) bool {
	if !digits(s) {
		return false
	}
	sum := 0
	for i := len(s) - 1; i >= 0; i-- {
		n := int(s[i] - '0')
		if (len(s)-1-i)%2 == 1 {
			n *= 2
			if n > 9 {
				n -= 9
			}
		}
		sum += n
	}
	return sum%10 == 0
}

func mod1110Valid(s string) bool {
	if !digits(s) {
		return false
	}
	product := 10
	for i := 0; i < len(s)-1; i++ {
		sum := (product + int(s[i]-'0')) % 10
		if sum == 0 {
			sum = 10
		}
		product = sum * 2 % 11
	}
	return (11-product)%10 == int(s[len(s)-1]-'0')
}

func mod97Valid(s string) bool {
	if !digits(s) {
		return false
	}
	remainder := 0
	for i := range s {
		remainder = (remainder*10 + int(s[i]-'0')) % 97
	}
	return remainder == 1
}

func ibanValid(s string) bool {
	if len(s) > 1024 {
		return false
	}
	s = strings.ToUpper(strings.ReplaceAll(s, " ", ""))
	if len(s) < 15 || len(s) > 34 || s[0] < 'A' || s[0] > 'Z' || s[1] < 'A' || s[1] > 'Z' || !digits(s[2:4]) {
		return false
	}
	if s[2:4] < "02" || s[2:4] > "98" {
		return false
	}
	remainder := 0
	for _, c := range s[4:] + s[:4] {
		switch {
		case c >= '0' && c <= '9':
			remainder = (remainder*10 + int(c-'0')) % 97
		case c >= 'A' && c <= 'Z':
			remainder = (remainder*100 + int(c-'A') + 10) % 97
		default:
			return false
		}
	}
	return remainder == 1
}
