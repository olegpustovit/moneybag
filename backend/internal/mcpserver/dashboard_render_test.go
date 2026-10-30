package mcpserver

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRenderOutputAddsImageOnlyWhenRendered(t *testing.T) {
	text := renderOutput{Message: "image unavailable: renderer not configured"}.content()
	if len(text) != 1 {
		t.Fatalf("text-only content: %d items", len(text))
	}
	image := renderOutput{PNG: []byte{0x89, 'P', 'N', 'G'}}.content()
	if len(image) != 2 {
		t.Fatalf("image content: %d items", len(image))
	}
	if _, ok := image[1].(*mcp.ImageContent); !ok {
		t.Fatalf("second item is %T, want image", image[1])
	}
}
