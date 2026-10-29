package handlers

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The design language is square: every served asset may only set border-radius to 0.
func TestServedAssetsUseSquareCorners(t *testing.T) {
	files, _ := filepath.Glob("web/*")
	if len(files) == 0 {
		t.Fatal("no web assets found")
	}
	radius := regexp.MustCompile(`(?i)border(-[a-z]+)*-radius\s*:\s*([^;}"']*)|borderRadius`)
	for _, f := range files {
		b, e := os.ReadFile(f)
		if e != nil {
			t.Fatal(e)
		}
		for _, m := range radius.FindAllStringSubmatch(string(b), -1) {
			if v := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(m[2]), "!important")); m[2] == "" || v != "0" {
				t.Errorf("%s: non-zero radius %q", f, m[0])
			}
		}
	}
}
