package dashboard

import (
	"encoding/json"
	"fmt"
)

type byteBudget struct{ remaining int }

func (b *byteBudget) Write(p []byte) (int, error) {
	if len(p) > b.remaining {
		return 0, fmt.Errorf("query data exceeds 8 MiB")
	}
	b.remaining -= len(p)
	return len(p), nil
}
func boundedData(v any) error { return json.NewEncoder(&byteBudget{8 * 1024 * 1024}).Encode(v) }

// spend charges v's encoded size against a shared budget, so a growing map is measured once per item rather than re-encoded whole.
func (b *byteBudget) spend(v any) error { return json.NewEncoder(b).Encode(v) }
