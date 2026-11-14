package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oklog/ulid/v2"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"time"
)

type Target struct {
	Collection string `json:"coll" db:"coll"`
	ID         string `json:"id" db:"id"`
}
type Event struct {
	UserID    string          `json:"-" db:"user_id"`
	ID        string          `json:"id" db:"id"`
	Seq       int64           `json:"seq" db:"seq"`
	At        time.Time       `json:"at" db:"at"`
	Actor     principal.Actor `json:"actor" db:"actor"`
	Tool      string          `json:"tool" db:"tool"`
	Target    Target          `json:"target" db:"target"`
	Before    json.RawMessage `json:"before,omitempty" db:"before"`
	After     json.RawMessage `json:"after,omitempty" db:"after"`
	Reason    string          `json:"reason,omitempty" db:"reason"`
	GrantID   string          `json:"grant_id,omitempty" db:"grant_id"`
	GrantHash string          `json:"grant_hash,omitempty" db:"grant_hash"`
	// Summary, BatchID and Routine stay out of the hash when empty, so events written before they existed still verify.
	Summary  string   `json:"summary,omitempty" db:"summary"`
	BatchID  string   `json:"batch_id,omitempty" db:"batch_id"`
	Routine  bool     `json:"routine,omitempty" db:"routine"`
	PrevHash string   `json:"prev_hash" db:"prev_hash"`
	Hash     string   `json:"hash" db:"hash"`
	Outcome  *Outcome `json:"outcome,omitempty" db:"-"`
}
type Outcome struct {
	Hash   string          `json:"hash" db:"hash"`
	ID     string          `json:"event_id" db:"id"`
	UserID string          `json:"-" db:"user_id"`
	Status string          `json:"status" db:"status"`
	Before json.RawMessage `json:"before,omitempty" db:"before"`
	After  json.RawMessage `json:"after,omitempty" db:"after"`
	At     time.Time       `json:"at" db:"at"`
}
type PostgresRepository struct{ DB *pgxpool.Pool }
type Service struct {
	repo *PostgresRepository
	// OnChange is configured before serving requests; false starts a write, true finishes it.
	OnChange func(userID string, finished bool)
}

func NewPostgresRepository(db *pgxpool.Pool) *PostgresRepository { return &PostgresRepository{db} }
func NewService(r *PostgresRepository) *Service                  { return &Service{repo: r} }

type grantContextKey struct{}
type grantContext struct{ id, hash string }

// WithGrant binds an audited shared mutation to the grant that authorized it.
func WithGrant(ctx context.Context, id, hash string) context.Context {
	return context.WithValue(ctx, grantContextKey{}, grantContext{id, hash})
}

type noteContextKey struct{}

// Note carries the readable-feed fields of the next audited writes.
type Note struct {
	Summary, BatchID string
	Routine          bool
}

// WithSummary sets the human summary of the writes under ctx; without it Write derives one from the tool and payload.
func WithSummary(ctx context.Context, summary string) context.Context {
	note, _ := ctx.Value(noteContextKey{}).(Note)
	note.Summary = summary
	return context.WithValue(ctx, noteContextKey{}, note)
}

// WithBatch makes the writes under ctx collapse into one line of the changes feed.
func WithBatch(ctx context.Context, id string) context.Context {
	note, _ := ctx.Value(noteContextKey{}).(Note)
	note.BatchID = id
	return context.WithValue(ctx, noteContextKey{}, note)
}

// WithRoutine marks the writes under ctx as system housekeeping, hidden from the default changes feed.
func WithRoutine(ctx context.Context) context.Context {
	note, _ := ctx.Value(noteContextKey{}).(Note)
	note.Routine = true
	return context.WithValue(ctx, noteContextKey{}, note)
}

const eventColumns = `events.user_id,events.id,events.seq,events.at,events.actor,events.tool,events.target,
 events.before,events.after,events.reason,events.grant_id,events.grant_hash,events.summary,events.batch_id,events.routine,events.prev_hash,events.hash`

func (s *Service) Workflow(ctx context.Context, fn func(context.Context) error) error {
	return postgres.Workflow(ctx, fn)
}
func (s *Service) Write(ctx context.Context, p principal.Principal, tool string, target Target, reason string, before, after any, fn func(context.Context) error) (string, error) {
	db := s.repo.DB
	if err := p.Valid(); err != nil {
		return "", err
	}
	event := Event{UserID: p.UserID, ID: ulid.Make().String(), At: time.Now().UTC().Truncate(time.Millisecond), Actor: p.Actor, Tool: tool, Target: target, Reason: reason}
	if grant, ok := ctx.Value(grantContextKey{}).(grantContext); ok {
		event.GrantID, event.GrantHash = grant.id, grant.hash
	}
	note, _ := ctx.Value(noteContextKey{}).(Note)
	event.Summary, event.BatchID, event.Routine = note.Summary, note.BatchID, note.Routine
	err := postgres.Workflow(ctx, func(tx context.Context) error {
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
		if event.Summary == "" {
			event.Summary = Summarize(event)
		}
		seq, err := postgres.Next(tx, db, p.UserID+":audit")
		if err != nil {
			return err
		}
		event.Seq = seq
		const previousSQL = `SELECT hash FROM events WHERE user_id=@user ORDER BY seq DESC LIMIT 1`
		var prev Event
		err = db.QueryRow(tx, previousSQL, pgx.NamedArgs{"user": p.UserID}).Scan(&prev.Hash)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		event.PrevHash = prev.Hash
		event.Hash = Digest(event)
		const insertSQL = `INSERT INTO events (user_id,id,seq,at,actor,actor_id,tool,target,target_id,before,after,reason,grant_id,grant_hash,summary,batch_id,routine,prev_hash,hash)
 VALUES (@user,@id,@seq,@at,@actor,@actor_id,@tool,@target,@target_id,@before,@after,@reason,@grant,@grant_hash,@summary,@batch,@routine,@prev,@hash)`
		_, err = db.Exec(tx, insertSQL, pgx.NamedArgs{"user": event.UserID, "id": event.ID, "seq": event.Seq, "at": event.At, "actor": event.Actor, "actor_id": event.Actor.ID, "tool": event.Tool, "target": event.Target, "target_id": event.Target.ID, "before": []byte(event.Before), "after": []byte(event.After), "reason": event.Reason, "grant": event.GrantID, "grant_hash": event.GrantHash, "summary": event.Summary, "batch": event.BatchID, "routine": event.Routine, "prev": event.PrevHash, "hash": event.Hash})
		if err != nil {
			return err
		}
		// A nil fn records a read: nothing changed, so cached views stay valid.
		var writeErr error
		if fn != nil {
			writeErr = s.Mutation(tx, p, fn)
		}
		beforeJSON, encodeErr := json.Marshal(before)
		if encodeErr != nil {
			return encodeErr
		}
		afterJSON, encodeErr := json.Marshal(after)
		if encodeErr != nil {
			return encodeErr
		}
		outcome := Outcome{ID: event.ID, UserID: p.UserID, Status: "completed", At: time.Now().UTC().Truncate(time.Millisecond)}
		// The event already holds both payloads; the outcome keeps one only when the write changed it.
		if !bytes.Equal(beforeJSON, event.Before) {
			outcome.Before = beforeJSON
		}
		if !bytes.Equal(afterJSON, event.After) {
			outcome.After = afterJSON
		}
		if writeErr != nil {
			outcome.Status = "failed"
		}
		outcome.Hash = OutcomeDigest(event.Hash, outcome)
		finish, stop := context.WithTimeout(context.WithoutCancel(tx), 5*time.Second)
		const outcomeSQL = `INSERT INTO audit_outcomes (hash,id,user_id,status,before,after,at) VALUES (@hash,@id,@user,@status,@before,@after,@at)`
		_, err = db.Exec(finish, outcomeSQL, pgx.NamedArgs{"hash": outcome.Hash, "id": outcome.ID, "user": outcome.UserID, "status": outcome.Status, "before": []byte(outcome.Before), "after": []byte(outcome.After), "at": outcome.At})
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

// Result returns the final before/after payloads, preferring what the outcome recorded.
func (e Event) Result() (before, after json.RawMessage) {
	before, after = e.Before, e.After
	if e.Outcome != nil && len(e.Outcome.Before) > 0 {
		before = e.Outcome.Before
	}
	if e.Outcome != nil && len(e.Outcome.After) > 0 {
		after = e.Outcome.After
	}
	return before, after
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
	db := s.repo.DB
	if err := p.Valid(); err != nil {
		return Page{}, err
	}
	var from, to *time.Time
	if in.From != "" {
		d, e := time.Parse(time.RFC3339, in.From)
		if e != nil {
			return Page{}, fmt.Errorf("from must be RFC3339")
		}
		d = d.UTC().Truncate(time.Millisecond)
		from = &d
	}
	if in.To != "" {
		d, e := time.Parse(time.RFC3339, in.To)
		if e != nil {
			return Page{}, fmt.Errorf("to must be RFC3339")
		}
		d = d.UTC().Truncate(time.Millisecond)
		to = &d
	}
	limit := in.Limit
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 200 {
		return Page{}, fmt.Errorf("limit must be 1..200")
	}
	const listSQL = `SELECT ` + eventColumns + ` FROM events WHERE user_id=@user AND (@cursor::bigint<=0 OR seq<@cursor)
 AND (@tool='' OR tool=@tool) AND (@target='' OR target_id=@target) AND (@actor='' OR actor_id=@actor)
 AND (@from::timestamptz IS NULL OR at>=@from) AND (@to::timestamptz IS NULL OR at<=@to) ORDER BY seq DESC LIMIT @limit`
	rows, err := db.Query(ctx, listSQL, pgx.NamedArgs{"user": p.UserID, "cursor": in.Cursor, "tool": in.Tool, "target": in.Target, "actor": in.Actor, "from": from, "to": to, "limit": limit + 1})
	if err != nil {
		return Page{}, err
	}
	events, err := pgx.CollectRows(rows, pgx.RowToStructByName[Event])
	if err != nil {
		return Page{}, err
	}
	out := Page{Events: events}
	if len(out.Events) > limit {
		out.Events = out.Events[:limit]
		out.NextCursor = out.Events[limit-1].Seq
	}
	if err = s.attachOutcomes(ctx, p, out.Events); err != nil {
		return Page{}, err
	}
	return out, nil
}

// attachOutcomes reads a page's outcomes in one query and drops payloads older rows repeat from their event.
func (s *Service) attachOutcomes(ctx context.Context, p principal.Principal, events []Event) error {
	if len(events) == 0 {
		return nil
	}
	ids := make([]string, len(events))
	for i := range events {
		ids[i] = events[i].ID
	}
	const outcomesSQL = `SELECT * FROM audit_outcomes WHERE id=ANY(@ids::text[]) AND user_id=@user`
	rows, err := s.repo.DB.Query(ctx, outcomesSQL, pgx.NamedArgs{"ids": ids, "user": p.UserID})
	if err != nil {
		return err
	}
	outcomes, err := pgx.CollectRows(rows, pgx.RowToStructByName[Outcome])
	if err != nil {
		return err
	}
	byID := make(map[string]*Outcome, len(outcomes))
	for i := range outcomes {
		byID[outcomes[i].ID] = &outcomes[i]
	}
	for i := range events {
		o := byID[events[i].ID]
		if o == nil {
			continue
		}
		if bytes.Equal(o.Before, events[i].Before) {
			o.Before = nil
		}
		if bytes.Equal(o.After, events[i].After) {
			o.After = nil
		}
		events[i].Outcome = o
	}
	return nil
}

// HasLaterTarget reports whether a later write was recorded for the same target.
// LatestCompleted finds the newest completed attempt for a specific owned target.
func (s *Service) LatestCompleted(ctx context.Context, p principal.Principal, tool string, target Target) (*Event, error) {
	if err := p.Require("ledger:read"); err != nil {
		return nil, err
	}
	db := s.repo.DB
	if err := p.Valid(); err != nil {
		return nil, err
	}
	const latestSQL = `SELECT ` + eventColumns + `,o.hash,o.id,o.user_id,o.status,o.before,o.after,o.at
 FROM events JOIN audit_outcomes o ON o.id=events.id AND o.user_id=events.user_id
 WHERE events.user_id=@user AND events.tool=@tool AND events.target->>'coll'=@collection AND events.target_id=@target
 AND o.status='completed' ORDER BY events.seq DESC LIMIT 1`
	rows, err := db.Query(ctx, latestSQL, pgx.NamedArgs{"user": p.UserID, "tool": tool, "collection": target.Collection, "target": target.ID})
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	var event Event
	var outcome Outcome
	err = rows.Scan(&event.UserID, &event.ID, &event.Seq, &event.At, &event.Actor, &event.Tool, &event.Target,
		&event.Before, &event.After, &event.Reason, &event.GrantID, &event.GrantHash, &event.Summary, &event.BatchID, &event.Routine, &event.PrevHash, &event.Hash,
		&outcome.Hash, &outcome.ID, &outcome.UserID, &outcome.Status, &outcome.Before, &outcome.After, &outcome.At)
	if err != nil {
		return nil, err
	}
	event.Outcome = &outcome
	return &event, nil
}

// shortcut: failed or interrupted attempts also block undo, upgrade when outcomes can be checked efficiently.
func (s *Service) HasLaterTarget(ctx context.Context, p principal.Principal, target Target, seq int64) (bool, error) {
	if err := p.Require("ledger:read"); err != nil {
		return false, err
	}
	db := s.repo.DB
	if err := p.Valid(); err != nil {
		return false, err
	}
	const laterSQL = `SELECT EXISTS(SELECT 1 FROM events WHERE user_id=@user AND target->>'coll'=@collection AND target_id=@id AND seq>@seq)`
	var later bool
	err := db.QueryRow(ctx, laterSQL, pgx.NamedArgs{"user": p.UserID, "collection": target.Collection, "id": target.ID, "seq": seq}).Scan(&later)
	if err != nil {
		return false, fmt.Errorf("check later target events: %w", err)
	}
	return later, nil
}

// HasLaterGrantChange detects reissued or changed grants, even when their IDs are reused.
func (s *Service) HasLaterGrantChange(ctx context.Context, p principal.Principal, id string, seq int64) (bool, error) {
	if err := p.Require("ledger:read"); err != nil {
		return false, err
	}
	db := s.repo.DB
	if err := p.Valid(); err != nil {
		return false, err
	}
	const laterSQL = `SELECT EXISTS(SELECT 1 FROM events WHERE user_id=@user AND target->>'coll'='shares' AND target_id=@id AND seq>@seq AND tool=ANY(@tools::text[]))`
	var later bool
	err := db.QueryRow(ctx, laterSQL, pgx.NamedArgs{"user": p.UserID, "id": id, "seq": seq, "tools": []string{"dashboard_share", "dashboard_unshare"}}).Scan(&later)
	if err != nil {
		return false, fmt.Errorf("check later grant changes: %w", err)
	}
	return later, nil
}

func (s *Service) Get(ctx context.Context, p principal.Principal, id string) (Event, error) {
	if err := p.Require("ledger:read"); err != nil {
		return Event{}, err
	}
	db := s.repo.DB
	if err := p.Valid(); err != nil {
		return Event{}, err
	}
	const getSQL = `SELECT ` + eventColumns + ` FROM events WHERE user_id=@user AND id=@id`
	rows, err := db.Query(ctx, getSQL, pgx.NamedArgs{"user": p.UserID, "id": id})
	if err != nil {
		return Event{}, err
	}
	e, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[Event])
	if errors.Is(err, pgx.ErrNoRows) {
		err = principal.ErrNotFound
	}
	if err == nil {
		e.Outcome, err = s.outcome(ctx, p, id)
	}
	return e, err
}
func (s *Service) outcome(ctx context.Context, p principal.Principal, id string) (*Outcome, error) {
	rows, e := s.repo.DB.Query(ctx, `SELECT * FROM audit_outcomes WHERE id=@id AND user_id=@user`, pgx.NamedArgs{"id": id, "user": p.UserID})
	if e != nil {
		return nil, e
	}
	o, e := pgx.CollectOneRow(rows, pgx.RowToStructByName[Outcome])
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, nil
	}
	return &o, e
}
func (s *Service) Verify(ctx context.Context, p principal.Principal) error {
	db := s.repo.DB
	if err := p.Valid(); err != nil {
		return err
	}
	const verifySQL = `SELECT ` + eventColumns + ` FROM events WHERE user_id=@user ORDER BY seq`
	rows, err := db.Query(ctx, verifySQL, pgx.NamedArgs{"user": p.UserID})
	if err != nil {
		return err
	}
	defer rows.Close()
	var hash string
	var seq int64
	for rows.Next() {
		e, err := pgx.RowToStructByName[Event](rows)
		if err != nil {
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
	return rows.Err()
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
