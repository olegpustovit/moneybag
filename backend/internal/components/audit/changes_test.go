package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

// Events written before summary, batch_id and routine existed must keep their hash, or every existing chain stops verifying.
func TestDigestIgnoresEmptyNoteFields(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	event := Event{UserID: "user", ID: "event", Seq: 7, At: at, Actor: principal.Actor{Kind: "agent", ID: "client"}, Tool: "rate_set", Target: Target{"prices", "quote"}, Before: json.RawMessage(`null`), After: json.RawMessage(`{"pair":"EUR/USD"}`), PrevHash: "previous"}
	// The exact bytes the digest covered before the note fields were added.
	legacy := `{"user_id":"user","event":{"id":"event","seq":7,"at":"2026-01-02T03:04:05Z","actor":{"kind":"agent","id":"client"},"tool":"rate_set","target":{"coll":"prices","id":"quote"},"before":null,"after":{"pair":"EUR/USD"},"prev_hash":"previous","hash":""}}`
	sum := sha256.Sum256([]byte(legacy))
	if Digest(event) != hex.EncodeToString(sum[:]) {
		t.Fatal("an event without note fields no longer hashes as it did")
	}
	for name, change := range map[string]func(*Event){"summary": func(e *Event) { e.Summary = "Updated a rate" }, "batch": func(e *Event) { e.BatchID = "batch" }, "routine": func(e *Event) { e.Routine = true }} {
		changed := event
		change(&changed)
		if Digest(changed) == Digest(event) {
			t.Fatal(name, "is not protected by the hash")
		}
	}
}

func TestEveryToolHasReadableSummaryWithoutIDs(t *testing.T) {
	const ulid = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	for tool := range summaryRules {
		// A payload whose name fields hold machine text must fall back to the wording without a name.
		payload := json.RawMessage(`{"name":"` + ulid + `","title":"expenses:food","payee":"` + ulid + `","pair":"` + ulid + `","spec":{"title":"card_trend"}}`)
		summary := Summarize(Event{Tool: tool, Target: Target{"c", ulid}, Before: payload, After: payload})
		if summary == "" || strings.Contains(summary, ulid) || strings.Contains(summary, "expenses:") || strings.Contains(summary, "card_trend") || strings.Contains(summary, "%s") {
			t.Fatal(tool, summary)
		}
	}
	if got := Summarize(Event{Tool: "dashboard_patch", After: json.RawMessage(`{"spec":{"title":"Spending"}}`)}); got != "Edited the Spending dashboard" {
		t.Fatal(got)
	}
	if got := Summarize(Event{Tool: "brand_new_tool"}); got != "Changed brand new tool" {
		t.Fatal(got)
	}
}

func TestChangeGroupWording(t *testing.T) {
	g := ChangeGroup{Routine: true, Targets: []Target{}, Events: []ChangeEvent{}}
	e := Event{ID: "a", Tool: "rate_set", Summary: "Updated the EUR/USD rate", Routine: true}
	g.add(e, "completed")
	if g.Summary != "Updated the EUR/USD rate" || !g.Undoable || g.Count != 1 {
		t.Fatalf("%+v", g)
	}
	e.ID = "b"
	if !sameGroup(g, e) {
		t.Fatal("routine run did not group")
	}
	g.add(e, "failed")
	if g.Summary != "Updated 2 rate quotes" || g.Undoable || g.Status != "failed" {
		t.Fatalf("%+v", g)
	}
	if sameGroup(ChangeGroup{Events: []ChangeEvent{{Tool: "rate_set"}}}, Event{Tool: "rate_set"}) {
		t.Fatal("non-routine events grouped")
	}
	if !sameGroup(ChangeGroup{BatchID: "x", Events: []ChangeEvent{{Tool: "a"}}}, Event{Tool: "b", BatchID: "x"}) || sameGroup(ChangeGroup{BatchID: "x", Events: []ChangeEvent{{Tool: "a"}}}, Event{Tool: "a"}) {
		t.Fatal("batch grouping")
	}
}
