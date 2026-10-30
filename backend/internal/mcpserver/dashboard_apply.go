package mcpserver

import (
	"encoding/json"
	"fmt"
)

// withVersionGuard prepends a test op so the patch fails when the draft has moved past ifVersion.
func withVersionGuard(ops json.RawMessage, ifVersion int) (json.RawMessage, error) {
	var text string
	if json.Unmarshal(ops, &text) == nil {
		ops = json.RawMessage(text)
	}
	var list []json.RawMessage
	if err := json.Unmarshal(ops, &list); err != nil {
		return nil, fmt.Errorf("ops must be a JSON array")
	}
	guard := json.RawMessage(fmt.Sprintf(`{"op":"test","path":"/version","value":%d}`, ifVersion))
	return json.Marshal(append([]json.RawMessage{guard}, list...))
}
