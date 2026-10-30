package mcpserver

import (
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
)

// contentOutput lets a tool return image content next to its typed output.
type contentOutput interface {
	content() []mcp.Content
}

type renderOutput struct {
	Validation dashboard.Validation `json:"validation"`
	Message    string               `json:"message,omitempty"`
	PNG        []byte               `json:"-"`
}

func (o renderOutput) content() []mcp.Content {
	text, _ := json.Marshal(map[string]any{"ok": o.Validation.OK, "message": o.Message})
	out := []mcp.Content{&mcp.TextContent{Text: string(text)}}
	if len(o.PNG) > 0 {
		out = append(out, &mcp.ImageContent{Data: o.PNG, MIMEType: "image/png"})
	}
	return out
}
