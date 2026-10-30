package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/olegpustovit/moneybag/backend/internal/components/identity"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/mcpserver"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func testSPASettingsWrites(t *testing.T, ctx context.Context, endpoint string, owner principal.Principal, services mcpserver.Services, db *mongo.Database) {
	t.Helper()
	invite, err := services.Identity.Invite(ctx, owner, "settings-only@example.test")
	if err != nil {
		t.Fatal(err)
	}
	user, err := services.Identity.Register(ctx, invite, "Settings test", "synthetic-test-password")
	if err != nil {
		t.Fatal(err)
	}
	p := identity.Principal(user)
	if _, err = services.Ledger.CreateAccount(ctx, p, ledger.Account{ID: "assets:settings", Name: "Settings", Type: "asset", Commodities: []string{"EUR"}}); err != nil {
		t.Fatal(err)
	}
	raw, session, err := services.Identity.StartSession(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Collection("oauth_grants").InsertOne(ctx, bson.M{"_id": "settings-test-grant", "user_id": user.ID, "client_id": "settings-test-client", "client_name": "Synthetic", "expires_at": time.Now().Add(time.Hour), "revoked": false})
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	post := func(path string, form url.Values, jsonAccept bool) (int, map[string]any, http.Header) {
		t.Helper()
		req, e := http.NewRequestWithContext(ctx, "POST", endpoint+path, strings.NewReader(form.Encode()))
		if e != nil {
			t.Fatal(e)
		}
		req.AddCookie(&http.Cookie{Name: "moneybag_session", Value: raw})
		req.Header.Set("X-CSRF-Token", session.CSRF)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if jsonAccept {
			req.Header.Set("Accept", "application/json")
		}
		res, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		var body map[string]any
		if strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") {
			if e = json.NewDecoder(res.Body).Decode(&body); e != nil {
				t.Fatal(e)
			}
		}
		return res.StatusCode, body, res.Header
	}
	status, body, _ := post("/settings/revoke", url.Values{"id": {"settings-test-grant"}}, true)
	if status != 200 || body["ok"] != true {
		t.Fatalf("JSON revoke: %d %v", status, body)
	}
	var grant struct {
		Revoked bool `bson:"revoked"`
	}
	if err = db.Collection("oauth_grants").FindOne(ctx, bson.M{"_id": "settings-test-grant"}).Decode(&grant); err != nil || !grant.Revoked {
		t.Fatalf("grant not revoked: %v", err)
	}
	feed := url.Values{"id": {"wise"}, "url": {"https://example.com/statement.csv"}, "account": {"assets:settings"}, "format": {"generic_csv"}, "token": {"synthetic-token"}}
	status, body, _ = post("/settings/feed", feed, true)
	if status != 200 || body["ok"] != true {
		t.Fatalf("JSON feed: %d %v", status, body)
	}
	feeds, err := services.Imports.Feeds(ctx, p)
	if err != nil || len(feeds) != 1 || feeds[0].ID != "wise" || feeds[0].Account != "assets:settings" {
		t.Fatalf("saved feed: %v %v", feeds, err)
	}
	encoded, err := json.Marshal(feeds)
	if err != nil || strings.Contains(string(encoded), "synthetic-token") {
		t.Fatal("feed secret appeared in JSON")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint+"/api/settings", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: "moneybag_session", Value: raw})
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil || res.StatusCode != 200 || strings.Contains(string(response), "synthetic-token") || !strings.Contains(string(response), `"id":"wise"`) {
		t.Fatal("settings response missing feed or exposing its credential")
	}
	status, _, headers := post("/settings/feed", feed, false)
	if status != http.StatusSeeOther || headers.Get("Location") != "/settings" {
		t.Fatalf("form feed: %d %s", status, headers.Get("Location"))
	}
	status, body, _ = post("/settings/password", url.Values{"current": {"synthetic-test-password"}, "next": {"replacement-test-password"}}, true)
	if status != 200 || body["ok"] != true || body["redirect"] != "/login" {
		t.Fatalf("JSON password: %d %v", status, body)
	}
	if _, _, err = services.Identity.Session(ctx, raw); err == nil {
		t.Fatal("old session survived password change")
	}
	if _, err = services.Identity.Login(ctx, user.Email, "replacement-test-password"); err != nil {
		t.Fatal(fmt.Errorf("new password login: %w", err))
	}
	if _, err = services.Identity.Login(ctx, user.Email, "synthetic-test-password"); err == nil {
		t.Fatal("old password still accepted")
	}
}
