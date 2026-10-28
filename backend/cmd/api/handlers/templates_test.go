package handlers

import (
	"html/template"
	"io"
	"testing"
)

func TestApplicationTemplateStates(t *testing.T) {
	templates, e := template.ParseFS(webFS, "web/*.html")
	if e != nil {
		t.Fatal(e)
	}
	for _, data := range []pageData{{IsAccounts: true}, {IsDashboard: true, CanEditDashboard: true}, {IsDashboard: true, IsShared: true}, {IsImports: true, IsWorkflow: true}, {IsImportPreview: true, IsWorkflow: true}, {IsReconcile: true, IsWorkflow: true}, {IsSharedList: true, IsWorkflow: true}, {IsSettings: true}, {IsAudit: true}, {IsModel: true}} {
		if e = templates.ExecuteTemplate(io.Discard, "app.html", data); e != nil {
			t.Fatal(e)
		}
	}
}
