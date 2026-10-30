package mcpserver

import (
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/components/preview"
)

// contentOutput lets a tool return image content next to its typed output.
type contentOutput interface {
	content() []mcp.Content
}

type renderInput struct {
	ID      string         `json:"id"`
	Version any            `json:"version,omitempty"`
	Width   int            `json:"width,omitempty" jsonschema:"viewport width in CSS pixels, 320..3840; default 1440"`
	Theme   string         `json:"theme,omitempty" jsonschema:"light or dark; default light"`
	Params  map[string]any `json:"params,omitempty" jsonschema:"dashboard parameters, for example from, to or month"`
}

func (in renderInput) validate() error {
	if in.Width != 0 && (in.Width < 320 || in.Width > 3840) {
		return fmt.Errorf("width must be 320..3840")
	}
	if raw, err := json.Marshal(in.Params); err != nil || len(raw) > 4096 {
		return fmt.Errorf("params must be JSON under 4 KiB")
	}
	return nil
}

type renderOutput struct {
	Validation dashboard.Validation `json:"validation"`
	Lint       any                  `json:"lint" jsonschema:"same shape as dashboard_lint"`
	Tests      dashboard.TestReport `json:"tests"`
	Layout     []preview.Box        `json:"layout,omitempty"`
	Checks     *preview.Checks      `json:"checks,omitempty"`
	TimingMS   float64              `json:"timing_ms,omitempty"`
	RenderMS   int64                `json:"render_ms,omitempty"`
	Message    string               `json:"message,omitempty"`
	PNG        []byte               `json:"-"`
}

// Clients that ignore structured content still get every finding next to the image.
func (o renderOutput) content() []mcp.Content {
	text, _ := json.Marshal(o)
	out := []mcp.Content{&mcp.TextContent{Text: string(text)}}
	if len(o.PNG) > 0 {
		out = append(out, &mcp.ImageContent{Data: o.PNG, MIMEType: "image/png"})
	}
	return out
}
