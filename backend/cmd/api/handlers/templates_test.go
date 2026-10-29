package handlers

import (
	"io"
	"strings"
	"testing"
)

func TestApplicationTemplateStates(t *testing.T) {
	templates, e := parseTemplates()
	if e != nil {
		t.Fatal(e)
	}
	for _, data := range []pageData{{IsAccounts: true}, {IsDashboard: true, CanEditDashboard: true}, {IsDashboard: true, IsShared: true}, {IsImports: true, IsWorkflow: true}, {IsImportPreview: true, IsWorkflow: true}, {IsReconcile: true, IsWorkflow: true}, {IsSharedList: true, IsWorkflow: true}, {IsSettings: true}, {IsAudit: true}, {IsModel: true}} {
		if e = templates.ExecuteTemplate(io.Discard, "app.html", data); e != nil {
			t.Fatal(e)
		}
	}
	var rendered strings.Builder
	if e := templates.ExecuteTemplate(&rendered, "auth.html", authPage{}); e != nil {
		t.Fatal(e)
	}
	for _, path := range assetPaths {
		if !strings.Contains(rendered.String(), path) {
			t.Fatalf("missing fingerprinted asset %q", path)
		}
	}
}
