package audit

import (
	"context"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/mongodb"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"slices"
	"strings"
	"time"
)

type ChangesInput struct {
	From, To, Q string
	Routine     bool
	Cursor      int64
	Limit       int
}
type ChangeActor struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
}
type ChangeEvent struct {
	ID      string    `json:"id"`
	Seq     int64     `json:"seq"`
	At      time.Time `json:"at"`
	Tool    string    `json:"tool"`
	Target  Target    `json:"target"`
	Summary string    `json:"summary"`
	Reason  string    `json:"reason,omitempty"`
	Status  string    `json:"status"`
	Hash    string    `json:"hash"`
}
type ChangeGroup struct {
	ID       string        `json:"id"`
	At       time.Time     `json:"at"`
	Actor    ChangeActor   `json:"actor"`
	Summary  string        `json:"summary"`
	Count    int           `json:"count"`
	Routine  bool          `json:"routine"`
	BatchID  string        `json:"batch_id,omitempty"`
	Status   string        `json:"status"`
	Undoable bool          `json:"undoable"`
	Targets  []Target      `json:"targets"`
	Events   []ChangeEvent `json:"events"`
	actorID  string
}
type ChangesPage struct {
	Groups []ChangeGroup `json:"groups"`
	Next   int64         `json:"next,omitempty"`
}

// undoable lists the tools history.Service.inverse can invert.
var undoable = []string{"entry_post", "entry_reverse", "transfer_record", "reclassify", "entry_update", "entry_draft_status", "entry_commit", "reconcile_resolve", "balance_assert", "assertion_archive", "import_start", "import_status", "import_undo", "import_redo", "import_commit", "import_edit", "account_create", "account_update", "tags_set", "budget_set_line", "budget_archive", "rule_upsert", "rate_set", "rate_archive", "db_create", "db_archive", "db_alter", "db_update_cells", "scenario_upsert", "scenario_archive", "income_model_set", "dashboard_create", "dashboard_archive", "dashboard_publish", "dashboard_unpublish", "dashboard_add_component", "dashboard_update_component", "dashboard_remove_component", "dashboard_compose", "dashboard_patch", "dashboard_share", "dashboard_unshare", "metric_upsert", "label_set", "payee_alias_upsert", "annotation_upsert", "nav_set", "feedback_resolve", "user_disable"}

// routine also recognizes system rate writes from before the routine field existed.
func routine(e Event) bool { return e.Routine || e.Tool == "rate_set" && e.Actor.Kind == "system" }

// Changes reads the feed newest first, collapsing a batch, a run of routine writes by one tool and actor on one day,
// or consecutive writes by one actor and tool less than groupGap apart, into one group.
// shortcut: filters run in Go over at most 2500 events per page, so a sparse q can return a short page with next set; add a text index if the feed outgrows it.
func (s *Service) Changes(ctx context.Context, p principal.Principal, in ChangesInput, label func(principal.Actor) string) (ChangesPage, error) {
	if err := p.Require("ledger:read"); err != nil {
		return ChangesPage{}, err
	}
	db, err := mongodb.Shared(s.repo.DB, p)
	if err != nil {
		return ChangesPage{}, err
	}
	limit := in.Limit
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 200 || len(in.Q) > 200 {
		return ChangesPage{}, fmt.Errorf("limit must be 1..200 and q at most 200 characters")
	}
	f := bson.M{"user_id": p.UserID}
	dates := bson.M{}
	for field, value := range map[string]string{"$gte": in.From, "$lte": in.To} {
		if value == "" {
			continue
		}
		at, e := time.Parse(time.RFC3339, value)
		if e != nil {
			if at, e = time.Parse("2006-01-02", value); e != nil {
				return ChangesPage{}, fmt.Errorf("from and to must be YYYY-MM-DD or RFC3339")
			}
			if field == "$lte" {
				at = at.Add(24*time.Hour - time.Millisecond)
			}
		}
		dates[field] = at
	}
	if len(dates) > 0 {
		f["at"] = dates
	}
	if in.Cursor > 0 {
		f["seq"] = bson.M{"$lt": in.Cursor}
	}
	const batch = 500
	query := strings.ToLower(in.Q)
	out := ChangesPage{Groups: []ChangeGroup{}}
	for page := 0; page < 5; page++ {
		cur, err := db.Collection("events").Find(ctx, f, options.Find().SetSort(bson.D{{Key: "seq", Value: -1}}).SetLimit(batch))
		if err != nil {
			return ChangesPage{}, fmt.Errorf("read changes: %w", err)
		}
		var events []Event
		if err = cur.All(ctx, &events); err != nil {
			return ChangesPage{}, fmt.Errorf("decode changes: %w", err)
		}
		if len(events) == 0 {
			out.Next = 0
			return out, nil
		}
		statuses, err := s.statuses(ctx, p, events)
		if err != nil {
			return ChangesPage{}, err
		}
		for _, e := range events {
			if !in.Routine && routine(e) {
				out.Next = e.Seq
				continue
			}
			if e.Summary == "" {
				e.Summary = Summarize(e)
			}
			if query != "" && !strings.Contains(strings.ToLower(e.Summary), query) && !strings.Contains(strings.ToLower(e.Reason), query) {
				out.Next = e.Seq
				continue
			}
			if n := len(out.Groups); n > 0 && sameGroup(out.Groups[n-1], e) {
				out.Groups[n-1].add(e, statuses[e.ID])
				out.Next = e.Seq
				continue
			}
			if len(out.Groups) == limit {
				return out, nil
			}
			group := ChangeGroup{ID: e.ID, At: e.At, Actor: ChangeActor{Kind: e.Actor.Kind, Label: label(e.Actor)}, Routine: routine(e), BatchID: e.BatchID, actorID: e.Actor.ID, Targets: []Target{}, Events: []ChangeEvent{}}
			group.add(e, statuses[e.ID])
			out.Groups = append(out.Groups, group)
			out.Next = e.Seq
		}
		f["seq"] = bson.M{"$lt": events[len(events)-1].Seq}
		if len(events) < batch {
			out.Next = 0
			return out, nil
		}
	}
	return out, nil
}

const groupGap = 10 * time.Minute

// plurals words a run of one tool's writes; tools missing here fall back to "<summary>, N times".
var plurals = map[string]string{
	"entry_post": "Added %d entries", "entry_commit": "Posted %d entries", "entry_reverse": "Reversed %d entries", "entry_update": "Edited %d draft entries",
	"reclassify": "Recategorized %d entries", "transfer_record": "Recorded %d transfers", "tags_set": "Changed tags on %d entries",
	"import_edit": "Corrected %d staged statement rows", "db_update_cells": "Edited %d dataset rows", "rule_upsert": "Saved %d categorization rules",
	"balance_assert": "Recorded %d balance checks", "budget_set_line": "Changed %d budget lines", "account_create": "Added %d accounts",
}

func sameGroup(g ChangeGroup, e Event) bool {
	if e.BatchID != "" || g.BatchID != "" {
		return e.BatchID == g.BatchID
	}
	last := g.Events[len(g.Events)-1]
	if last.Tool == e.Tool && g.Actor.Kind == e.Actor.Kind && g.actorID == e.Actor.ID && last.At.Sub(e.At) < groupGap && e.At.Sub(last.At) < groupGap {
		return true
	}
	return g.Routine && routine(e) && last.Tool == e.Tool && g.Actor.Kind == e.Actor.Kind && g.At.Format("2006-01-02") == e.At.Format("2006-01-02")
}

func (g *ChangeGroup) add(e Event, status string) {
	g.Events = append(g.Events, ChangeEvent{e.ID, e.Seq, e.At, e.Tool, e.Target, e.Summary, e.Reason, status, e.Hash})
	if !slices.Contains(g.Targets, e.Target) {
		g.Targets = append(g.Targets, e.Target)
	}
	g.Count = len(g.Events)
	if g.Count == 1 || g.Status == "completed" && status != "completed" {
		g.Status = status
	}
	// A batch undoes event by event, so only a single-event group offers one undo.
	g.Undoable = g.Count == 1 && status == "completed" && slices.Contains(undoable, e.Tool)
	first := g.Events[0].Summary
	same, sameTool := true, true
	for _, event := range g.Events {
		same = same && event.Summary == first
		sameTool = sameTool && event.Tool == g.Events[0].Tool
	}
	rates, overview := true, ""
	for _, event := range g.Events {
		rates = rates && event.Tool == "rate_set"
		if event.Target.Collection == "import_batches" && overview == "" {
			overview = event.Summary
		}
	}
	switch {
	case g.Count == 1:
		g.Summary = first
	case overview != "":
		g.Summary = overview
	case rates:
		g.Summary = fmt.Sprintf("Updated %d rate quotes", g.Count)
	case sameTool && plurals[g.Events[0].Tool] != "":
		g.Summary = fmt.Sprintf(plurals[g.Events[0].Tool], g.Count)
	case same:
		g.Summary = fmt.Sprintf("%s, %d times", first, g.Count)
	default:
		g.Summary = fmt.Sprintf("%s, and %d more changes", g.Events[g.Count-1].Summary, g.Count-1)
	}
}

func (s *Service) statuses(ctx context.Context, p principal.Principal, events []Event) (map[string]string, error) {
	ids := make([]string, len(events))
	out := make(map[string]string, len(events))
	for i, e := range events {
		ids[i] = e.ID
		out[e.ID] = "interrupted"
	}
	cur, err := s.repo.DB.Collection("audit_outcomes").Find(ctx, bson.M{"user_id": p.UserID, "_id": bson.M{"$in": ids}}, options.Find().SetProjection(bson.M{"status": 1}))
	if err != nil {
		return nil, fmt.Errorf("read change outcomes: %w", err)
	}
	var rows []struct {
		ID     string `bson:"_id"`
		Status string `bson:"status"`
	}
	if err = cur.All(ctx, &rows); err != nil {
		return nil, fmt.Errorf("decode change outcomes: %w", err)
	}
	for _, row := range rows {
		out[row.ID] = row.Status
	}
	return out, nil
}
