package dashboard

import (
	"embed"
	"encoding/json"
)

//go:embed templates/specs.json
var templatesFS embed.FS
var Templates = func() map[string]Spec {
	b, e := templatesFS.ReadFile("templates/specs.json")
	if e != nil {
		panic(e)
	}
	var out map[string]Spec
	if e = json.Unmarshal(b, &out); e != nil {
		panic(e)
	}
	return out
}()
