package imports

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
)

// Plaid amounts are positive for outflows: https://plaid.com/docs/api/products/transactions/.
func plaidRecords(content []byte) ([][]string, error) {
	var envelope struct {
		Transactions []json.RawMessage `json:"transactions"`
		Total        int               `json:"total_transactions"`
	}
	var transactions []json.RawMessage
	if bytes.HasPrefix(bytes.TrimSpace(content), []byte("[")) {
		if e := json.Unmarshal(content, &transactions); e != nil {
			return nil, fmt.Errorf("invalid Plaid JSON")
		}
	} else {
		if e := json.Unmarshal(content, &envelope); e != nil || envelope.Transactions == nil {
			return nil, fmt.Errorf("Plaid input needs a transactions array")
		}
		transactions = envelope.Transactions
		if envelope.Total > len(transactions) {
			return nil, fmt.Errorf("Plaid response is paginated; export all transactions for one account before importing")
		}
	}
	if len(transactions) > 10000 {
		return nil, fmt.Errorf("statement exceeds 10000 rows")
	}
	records := [][]string{{"date", "amount", "currency", "description", "id", "account_id", "raw_json"}}
	account := ""
	for i, raw := range transactions {
		var v struct {
			ID         string      `json:"transaction_id"`
			Account    string      `json:"account_id"`
			Date       string      `json:"date"`
			Amount     json.Number `json:"amount"`
			Currency   string      `json:"iso_currency_code"`
			Unofficial string      `json:"unofficial_currency_code"`
			Name       string      `json:"name"`
			Merchant   string      `json:"merchant_name"`
			Pending    bool        `json:"pending"`
		}
		if e := json.Unmarshal(raw, &v); e != nil {
			return nil, fmt.Errorf("invalid Plaid transaction %d", i+1)
		}
		if v.Pending {
			continue
		}
		if v.ID == "" || v.Account == "" {
			return nil, fmt.Errorf("Plaid transaction %d needs transaction_id and account_id", i+1)
		}
		if account != "" && account != v.Account {
			return nil, fmt.Errorf("Plaid import must contain one account; split this export by account_id")
		}
		account = v.Account
		amount, e := ledger.ParseDecimal(v.Amount.String())
		if e != nil {
			return nil, fmt.Errorf("invalid Plaid amount at transaction %d", i+1)
		}
		if v.Currency == "" {
			v.Currency = v.Unofficial
		}
		payee := v.Merchant
		if payee == "" {
			payee = v.Name
		}
		records = append(records, []string{v.Date, amount.Negate().String(), v.Currency, payee, v.ID, v.Account, string(raw)})
	}
	return records, nil
}
