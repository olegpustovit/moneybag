package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/oklog/ulid/v2"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/mongodb"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"time"
)

type Target struct {
	Collection string `json:"coll" bson:"coll"`
	ID         string `json:"id" bson:"id"`
}
type Event struct {
	UserID   string          `json:"-" bson:"user_id"`
	ID       string          `json:"id" bson:"_id"`
	Seq      int64           `json:"seq" bson:"seq"`
	At       time.Time       `json:"at" bson:"at"`
	Actor    principal.Actor `json:"actor" bson:"actor"`
	Tool     string          `json:"tool" bson:"tool"`
	Target   Target          `json:"target" bson:"target"`
	Before   json.RawMessage `json:"before,omitempty" bson:"before,omitempty"`
	After    json.RawMessage `json:"after,omitempty" bson:"after,omitempty"`
	Reason   string          `json:"reason,omitempty" bson:"reason,omitempty"`
	PrevHash string          `json:"prev_hash" bson:"prev_hash"`
	Hash     string          `json:"hash" bson:"hash"`
	Outcome  *Outcome        `json:"outcome,omitempty" bson:"-"`
}
type Outcome struct {
	Hash   string          `json:"hash" bson:"hash"`
	ID     string          `json:"event_id" bson:"_id"`
	UserID string          `json:"-" bson:"user_id"`
	Status string          `json:"status" bson:"status"`
	Before json.RawMessage `json:"before,omitempty" bson:"before,omitempty"`
	After  json.RawMessage `json:"after,omitempty" bson:"after,omitempty"`
	At     time.Time       `json:"at" bson:"at"`
}
type MongoRepository struct{ DB *mongo.Database }
type Service struct {
	repo *MongoRepository
	// OnChange is configured before serving requests; false starts a write, true finishes it.
	OnChange func(userID string, finished bool)
}

func NewMongoRepository(db *mongo.Database) *MongoRepository { return &MongoRepository{db} }
func NewService(r *MongoRepository) *Service                 { return &Service{repo: r} }
func (s *Service) Workflow(ctx context.Context, fn func(context.Context) error) error {
	return mongodb.Workflow(ctx, fn)
}
func (s *Service) Write(ctx context.Context, p principal.Principal, tool string, target Target, reason string, before, after any, fn func(context.Context) error) (string, error) {
	db, err := mongodb.Shared(s.repo.DB, p)
	if err != nil {
		return "", err
	}
	event := Event{UserID: p.UserID, ID: ulid.Make().String(), At: time.Now().UTC().Truncate(time.Millisecond), Actor: p.Actor, Tool: tool, Target: target, Reason: reason}
	err = mongodb.Workflow(ctx, func(tx context.Context) error {
		b, err := json.Marshal(before)
		if err != nil {
			return err
		}
		event.Before = b
		a, err := json.Marshal(after)
		if err != nil {
			return err
		}
		event.After = a
		seq, err := mongodb.Next(tx, db, p.UserID+":audit")
		if err != nil {
			return err
		}
		event.Seq = seq
		var prev Event
		err = db.Collection("events").FindOne(tx, bson.M{"user_id": p.UserID}, options.FindOne().SetSort(bson.D{{Key: "seq", Value: -1}})).Decode(&prev)
		if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
			return err
		}
		event.PrevHash = prev.Hash
		event.Hash = Digest(event)
		_, err = db.Collection("events").InsertOne(tx, event)
		if err != nil {
			return err
		}
		writeErr := s.Mutation(tx, p, fn)
		beforeJSON, encodeErr := json.Marshal(before)
		if encodeErr != nil {
			return encodeErr
		}
		afterJSON, encodeErr := json.Marshal(after)
		if encodeErr != nil {
			return encodeErr
		}
		outcome := Outcome{ID: event.ID, UserID: p.UserID, Status: "completed", Before: beforeJSON, After: afterJSON, At: time.Now().UTC().Truncate(time.Millisecond)}
		if writeErr != nil {
			outcome.Status = "failed"
		}
		outcome.Hash = OutcomeDigest(event.Hash, outcome)
		finish, stop := context.WithTimeout(context.WithoutCancel(tx), 5*time.Second)
		_, err = db.Collection("audit_outcomes").InsertOne(finish, outcome)
		stop()
		if err != nil {
			return fmt.Errorf("audit outcome missing for %s; inspect history before retrying: %w", event.ID, err)
		}
		if writeErr != nil {
			return fmt.Errorf("workflow %s failed; inspect history before retrying: %w", event.ID, writeErr)
		}
		return nil
	})
	return event.ID, err
}
func Digest(e Event) string {
	e.Hash = ""
	e.Outcome = nil
	b, _ := json.Marshal(struct {
		UserID string `json:"user_id"`
		Event  Event  `json:"event"`
	}{e.UserID, e})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

type ListInput struct {
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
	Actor  string `json:"actor,omitempty"`
	Tool   string `json:"tool,omitempty"`
	Target string `json:"target,omitempty"`
	Cursor int64  `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}
type Page struct {
	Events     []Event `json:"events"`
	NextCursor int64   `json:"next_cursor,omitempty"`
}

func (s *Service) List(ctx context.Context, p principal.Principal, in ListInput) (Page, error) {
	if err := p.Require("ledger:read"); err != nil {
		return Page{}, err
	}
	db, err := mongodb.Shared(s.repo.DB, p)
	if err != nil {
		return Page{}, err
	}
	f := bson.M{"user_id": p.UserID}
	if in.Cursor > 0 {
		f["seq"] = bson.M{"$lt": in.Cursor}
	}
	if in.Tool != "" {
		f["tool"] = in.Tool
	}
	if in.Target != "" {
		f["target.id"] = in.Target
	}
	if in.Actor != "" {
		f["actor.id"] = in.Actor
	}
	dates := bson.M{}
	if in.From != "" {
		d, e := time.Parse(time.RFC3339, in.From)
		if e != nil {
			return Page{}, fmt.Errorf("from must be RFC3339")
		}
		dates["$gte"] = d
	}
	if in.To != "" {
		d, e := time.Parse(time.RFC3339, in.To)
		if e != nil {
			return Page{}, fmt.Errorf("to must be RFC3339")
		}
		dates["$lte"] = d
	}
	if len(dates) > 0 {
		f["at"] = dates
	}
	limit := in.Limit
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 200 {
		return Page{}, fmt.Errorf("limit must be 1..200")
	}
	cur, err := db.Collection("events").Find(ctx, f, options.Find().SetSort(bson.D{{Key: "seq", Value: -1}}).SetLimit(int64(limit+1)))
	if err != nil {
		return Page{}, err
	}
	defer cur.Close(ctx)
	out := Page{Events: []Event{}}
	if err = cur.All(ctx, &out.Events); err != nil {
		return Page{}, err
	}
	if len(out.Events) > limit {
		out.Events = out.Events[:limit]
		out.NextCursor = out.Events[limit-1].Seq
	}
	for i := range out.Events {
		out.Events[i].Outcome, err = s.outcome(ctx, p, out.Events[i].ID)
		if err != nil {
			return Page{}, err
		}
	}
	return out, nil
}
func (s *Service) Get(ctx context.Context, p principal.Principal, id string) (Event, error) {
	if err := p.Require("ledger:read"); err != nil {
		return Event{}, err
	}
	db, err := mongodb.Shared(s.repo.DB, p)
	if err != nil {
		return Event{}, err
	}
	var e Event
	err = db.Collection("events").FindOne(ctx, bson.M{"user_id": p.UserID, "_id": id}).Decode(&e)
	if errors.Is(err, mongo.ErrNoDocuments) {
		err = principal.ErrNotFound
	}
	if err == nil {
		e.Outcome, err = s.outcome(ctx, p, id)
	}
	return e, err
}
func (s *Service) outcome(ctx context.Context, p principal.Principal, id string) (*Outcome, error) {
	var o Outcome
	e := s.repo.DB.Collection("audit_outcomes").FindOne(ctx, bson.M{"_id": id, "user_id": p.UserID}).Decode(&o)
	if errors.Is(e, mongo.ErrNoDocuments) {
		return nil, nil
	}
	return &o, e
}
func (s *Service) Verify(ctx context.Context, p principal.Principal) error {
	db, err := mongodb.Shared(s.repo.DB, p)
	if err != nil {
		return err
	}
	cur, err := db.Collection("events").Find(ctx, bson.M{"user_id": p.UserID}, options.Find().SetSort(bson.D{{Key: "seq", Value: 1}}))
	if err != nil {
		return err
	}
	defer cur.Close(ctx)
	var hash string
	var seq int64
	for cur.Next(ctx) {
		var e Event
		if err = cur.Decode(&e); err != nil {
			return err
		}
		if e.PrevHash != hash || e.Hash != Digest(e) || e.Seq <= seq {
			return fmt.Errorf("audit chain invalid at %d", e.Seq)
		}
		outcome, err := s.outcome(ctx, p, e.ID)
		if err != nil {
			return err
		}
		if outcome == nil {
			return fmt.Errorf("workflow %s is missing its outcome; review before retrying", e.ID)
		}
		if outcome.Hash != OutcomeDigest(e.Hash, *outcome) {
			return fmt.Errorf("audit outcome invalid for %s", e.ID)
		}
		hash = e.Hash
		seq = e.Seq
	}
	return cur.Err()
}

func OutcomeDigest(eventHash string, o Outcome) string {
	o.Hash = ""
	b, _ := json.Marshal(struct {
		EventHash, UserID string
		Outcome           Outcome
	}{eventHash, o.UserID, o})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
