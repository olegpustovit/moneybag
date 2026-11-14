package imports

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oklog/ulid/v2"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/classify"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/reconcile"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/safefetch"
	"net/http"
	"slices"
	"strings"
	"time"
)

type Candidate struct {
	RowID     string `json:"row_id" bson:"row_id"`
	Duplicate bool   `json:"duplicate" bson:"duplicate"`
}
type Batch struct {
	ID             string         `json:"id" bson:"_id" db:"id"`
	UserID         string         `json:"-" bson:"user_id" db:"user_id"`
	Account        string         `json:"account" bson:"account" db:"account"`
	Format         string         `json:"format" bson:"format" db:"format"`
	FileSHA256     string         `json:"file_sha256" bson:"file_sha256" db:"file_sha256"`
	Status         string         `json:"status" bson:"status" db:"status"`
	Candidates     []Candidate    `json:"-" bson:"candidates" db:"candidates"`
	From           string         `json:"from" bson:"from" db:"from"`
	To             string         `json:"to" bson:"to" db:"to"`
	ClosingBalance *ledger.Amount `json:"closing_balance,omitempty" bson:"closing_balance,omitempty" db:"closing_balance"`
	CreatedAt      time.Time      `json:"created_at" bson:"created_at" db:"created_at"`
}
type ImportRow struct {
	ID         string            `json:"id" bson:"_id" db:"id"`
	UserID     string            `json:"-" bson:"user_id" db:"user_id"`
	BatchID    string            `json:"batch_id" bson:"batch_id" db:"batch_id"`
	Account    string            `json:"account" bson:"account" db:"account"`
	DedupeKey  string            `json:"dedupe_key" bson:"dedupe_key" db:"dedupe_key"`
	Raw        map[string]string `json:"raw" bson:"raw" db:"raw"`
	Proposed   ledger.EntryInput `json:"proposed" bson:"proposed" db:"proposed"`
	Kind       string            `json:"kind" bson:"kind" db:"kind"`
	EntryID    string            `json:"entry_id,omitempty" bson:"entry_id,omitempty" db:"entry_id"`
	ReversalID string            `json:"reversal_id,omitempty" bson:"reversal_id,omitempty" db:"reversal_id"`
	ProposedID string            `json:"-" bson:"proposed_id" db:"proposed_id"`
	Skip       bool              `json:"skip" bson:"skip" db:"skip"`
}
type StartInput struct {
	Account    string  `json:"account"`
	Format     string  `json:"format"`
	FileURL    string  `json:"file_url,omitempty"`
	ContentB64 string  `json:"content_b64,omitempty"`
	Mapping    Mapping `json:"mapping,omitempty"`
}
type StartResult struct {
	BatchID    string `json:"batch_id"`
	RowsParsed int    `json:"rows_parsed"`
	Period     struct {
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"period"`
}
type PreviewResult struct {
	Proposed         []ImportRow    `json:"proposed"`
	Duplicates       []ImportRow    `json:"duplicates"`
	Unmatched        []string       `json:"unmatched"`
	StatementBalance *ledger.Amount `json:"statement_balance,omitempty"`
	NextCursor       int            `json:"next_cursor,omitempty"`
}
type EditInput struct {
	BatchID string `json:"batch_id"`
	RowID   string `json:"row_id"`
	Set     struct {
		Account *string   `json:"account,omitempty"`
		Tags    *[]string `json:"tags,omitempty"`
		Skip    *bool     `json:"skip,omitempty"`
	} `json:"set"`
}
type CommitResult struct {
	Posted      int    `json:"posted"`
	Skipped     int    `json:"skipped"`
	AssertionID string `json:"assertion_id,omitempty"`
}
type PostgresRepository struct{ db *pgxpool.Pool }
type Service struct {
	repo      *PostgresRepository
	ledger    *ledger.Service
	classify  *classify.Service
	reconcile *reconcile.Service
	audit     *audit.Service
	secret    []byte
	http      *http.Client
}

func NewPostgresRepository(db *pgxpool.Pool) *PostgresRepository { return &PostgresRepository{db} }
func NewService(r *PostgresRepository, l *ledger.Service, c *classify.Service, rec *reconcile.Service, a *audit.Service) *Service {
	return &Service{repo: r, ledger: l, classify: c, reconcile: rec, audit: a, http: safefetch.Client()}
}
func importWriter(p principal.Principal) principal.Principal {
	p.Scopes = append(append([]string{}, p.Scopes...), "ledger:read", "ledger:write")
	return p
}
func (s *Service) ensureAccount(ctx context.Context, p principal.Principal, id, name, kind, commodity string) error {
	a, e := s.ledger.Account(ctx, p, id)
	if errors.Is(e, principal.ErrNotFound) {
		_, e = s.ledger.CreateAccount(ctx, p, ledger.Account{ID: id, Name: name, Type: kind, Commodities: []string{commodity}})
		return e
	}
	if e != nil {
		return e
	}
	if !slices.Contains(a.Commodities, commodity) {
		a.Commodities = append(a.Commodities, commodity)
		_, e = s.ledger.UpdateAccount(ctx, p, ledger.AccountUpdate{ID: id, Commodities: &a.Commodities})
	}
	return e
}
func (s *Service) Start(ctx context.Context, p principal.Principal, in StartInput) (StartResult, error) {
	return s.start(ctx, p, in, "")
}
func (s *Service) start(ctx context.Context, p principal.Principal, in StartInput, since string) (StartResult, error) {
	if e := p.Require("import:write"); e != nil {
		return StartResult{}, e
	}
	if (in.FileURL == "") == (in.ContentB64 == "") {
		return StartResult{}, fmt.Errorf("provide exactly one file_url or content_b64")
	}
	var content []byte
	var e error
	if in.FileURL != "" {
		content, _, e = safefetch.Read(ctx, s.http, in.FileURL, 8*1024*1024)
	} else {
		content, e = base64.StdEncoding.DecodeString(in.ContentB64)
	}
	if e != nil {
		return StartResult{}, fmt.Errorf("could not read statement")
	}
	parsed, e := Parse(content, in.Format, in.Mapping)
	if e != nil {
		return StartResult{}, e
	}
	if since != "" {
		rows := []Parsed{}
		for _, row := range parsed {
			if row.Date >= since {
				rows = append(rows, row)
			}
		}
		parsed = rows
	}
	if len(parsed) == 0 {
		return StartResult{}, fmt.Errorf("statement contains no transactions")
	}
	writer := importWriter(p)
	account, e := s.ledger.Account(ctx, writer, in.Account)
	if e != nil {
		return StartResult{}, e
	}
	if account.Type != "asset" && account.Type != "liability" {
		return StartResult{}, fmt.Errorf("statement account must be an asset or liability")
	}
	for _, row := range parsed {
		if !slices.Contains(account.Commodities, row.Currency) {
			return StartResult{}, fmt.Errorf("account does not accept %s; add that commodity first", row.Currency)
		}
	}
	sum := sha256.Sum256(content)
	fileHash := hex.EncodeToString(sum[:])
	mapping, _ := json.Marshal(in.Mapping)
	keyHash := sha256.Sum256([]byte(in.Account + in.Format + fileHash + string(mapping) + since))
	batchID := p.UserID + ":" + hex.EncodeToString(keyHash[:])
	batch := Batch{ID: batchID, UserID: p.UserID, Account: in.Account, Format: in.Format, FileSHA256: fileHash, Status: "staging", Candidates: []Candidate{}, From: parsed[0].Date, To: parsed[0].Date, CreatedAt: time.Now().UTC().Truncate(time.Millisecond)}
	for _, row := range parsed {
		if row.Date < batch.From {
			batch.From = row.Date
		}
		if row.Date > batch.To {
			batch.To = row.Date
		}
		if row.Balance != nil && row.Date >= batch.To {
			batch.ClosingBalance = &ledger.Amount{Value: *row.Balance, Commodity: row.Currency}
		}
	}
	e = s.audit.Workflow(ctx, func(tx context.Context) error {
		existing, e := s.repo.batch(tx, p.UserID, batchID)
		if e == nil {
			batch = existing
			if batch.Status != "staging" {
				return nil
			}
		} else if !postgres.IsNotFound(e) {
			return e
		} else {
			if _, e = s.repo.db.Exec(tx, `INSERT INTO import_batches(id,user_id,account,format,file_sha256,status,candidates,"from","to",closing_balance,created_at) VALUES (@id,@user,@account,@format,@sha,@status,COALESCE(@candidates::jsonb,'null'::jsonb),@from,@to,@closing,@created)`, pgx.NamedArgs{"id": batch.ID, "user": p.UserID, "account": batch.Account, "format": batch.Format, "sha": batch.FileSHA256, "status": batch.Status, "candidates": batch.Candidates, "from": batch.From, "to": batch.To, "closing": batch.ClosingBalance, "created": batch.CreatedAt}); e != nil {
				return e
			}
			if _, e = s.repo.db.Exec(tx, `INSERT INTO import_files(id,user_id,content,sha256) VALUES (@id,@user,@content,@sha)`, pgx.NamedArgs{"id": batchID, "user": p.UserID, "content": content, "sha": fileHash}); e != nil {
				return e
			}
		}
		_, e = s.audit.Write(tx, p, "import_start", audit.Target{Collection: "import_batches", ID: batchID}, "", nil, &batch, func(tx context.Context) error {
			for i, row := range parsed {
				if i < len(batch.Candidates) {
					continue
				}
				rows, e := s.repo.db.Query(tx, `SELECT * FROM import_rows WHERE user_id=@user AND account=@account AND dedupe_key=@key`, pgx.NamedArgs{"user": p.UserID, "account": in.Account, "key": row.DedupeKey})
				if e != nil {
					return e
				}
				imported, e := pgx.CollectOneRow(rows, pgx.RowToStructByName[ImportRow])
				duplicate := false
				if e == nil {
					duplicate = imported.BatchID != batchID
					for _, c := range batch.Candidates {
						if c.RowID == imported.ID {
							duplicate = true
						}
					}
				} else if !postgres.IsNotFound(e) {
					return e
				} else {
					counterpart, name, kind := "expenses:unclassified", "Unclassified expenses", "expense"
					if row.Amount.Rat().Sign() > 0 {
						counterpart, name, kind = "income:unclassified", "Unclassified income", "income"
					}
					if mapped, e := mappedAccount(row.Category); e != nil {
						return e
					} else if mapped != "" {
						counterpart = mapped
					}
					if counterpart == "expenses:unclassified" || counterpart == "income:unclassified" {
						if e = s.ensureAccount(tx, writer, counterpart, name, kind, row.Currency); e != nil {
							return e
						}
					} else {
						if _, e = s.ledger.Account(tx, writer, counterpart); e != nil {
							return fmt.Errorf("mapped category is not an owned account: %w", e)
						}
					}
					proposed := ledger.EntryInput{Date: row.Date, Payee: statementPayee(row.Payee), Postings: []ledger.Posting{{Account: in.Account, Amount: ledger.Amount{Value: row.Amount, Commodity: row.Currency}}, {Account: counterpart, Amount: ledger.Amount{Value: row.Amount.Negate(), Commodity: row.Currency}}}, Tags: []string{}}
					rule, e := s.classify.Classify(tx, writer, ledger.Entry{Date: row.Date, Payee: row.Payee, Postings: proposed.Postings})
					if e != nil {
						return e
					}
					if rule.Account != "" {
						proposed.Postings[1].Account = rule.Account
					}
					proposed.Tags = rule.Tags
					if e = s.ledger.ValidateEntry(tx, writer, proposed); e != nil {
						return fmt.Errorf("invalid statement proposal: %w", e)
					}
					imported = ImportRow{ID: ulid.Make().String(), UserID: p.UserID, BatchID: batchID, Account: in.Account, DedupeKey: row.DedupeKey, Raw: row.Raw, Proposed: proposed, ProposedID: ulid.Make().String(), Kind: rule.Kind}
					if _, e = s.repo.db.Exec(tx, `INSERT INTO import_rows(id,user_id,batch_id,account,dedupe_key,raw,proposed,kind,entry_id,reversal_id,proposed_id,skip) VALUES (@id,@user,@batch,@account,@key,@raw,@proposed,@kind,@entry,@reversal,@proposal,@skip)`, pgx.NamedArgs{"id": imported.ID, "user": p.UserID, "batch": imported.BatchID, "account": imported.Account, "key": imported.DedupeKey, "raw": imported.Raw, "proposed": imported.Proposed, "kind": imported.Kind, "entry": imported.EntryID, "reversal": imported.ReversalID, "proposal": imported.ProposedID, "skip": imported.Skip}); e != nil {
						return e
					}
				}
				candidate := Candidate{RowID: imported.ID, Duplicate: duplicate}
				if _, e = s.repo.db.Exec(tx, `UPDATE import_batches SET candidates=COALESCE(NULLIF(candidates,'null'::jsonb),'[]'::jsonb)||jsonb_build_array(@candidate::jsonb) WHERE id=@id AND user_id=@user`, pgx.NamedArgs{"candidate": candidate, "id": batchID, "user": p.UserID}); e != nil {
					return e
				}
				batch.Candidates = append(batch.Candidates, candidate)
			}
			batch.Status = "ready"
			_, e := s.repo.db.Exec(tx, `UPDATE import_batches SET status=@status WHERE id=@id AND user_id=@user`, pgx.NamedArgs{"status": "ready", "id": batchID, "user": p.UserID})
			return e
		})
		return e
	})
	out := StartResult{BatchID: batch.ID, RowsParsed: len(batch.Candidates)}
	out.Period.From = batch.From
	out.Period.To = batch.To
	return out, e
}
func (s *Service) batch(ctx context.Context, p principal.Principal, id string) (Batch, error) {
	if e := p.Valid(); e != nil {
		return Batch{}, e
	}
	b, e := s.repo.batch(ctx, p.UserID, id)
	if postgres.IsNotFound(e) {
		e = principal.ErrNotFound
	}
	return b, e
}
func (s *Service) row(ctx context.Context, p principal.Principal, id string) (ImportRow, error) {
	var r ImportRow
	rows, e := s.repo.db.Query(ctx, `SELECT * FROM import_rows WHERE id=@id AND user_id=@user`, pgx.NamedArgs{"id": id, "user": p.UserID})
	if e != nil {
		return r, e
	}
	r, e = pgx.CollectOneRow(rows, pgx.RowToStructByName[ImportRow])
	if postgres.IsNotFound(e) {
		e = principal.ErrNotFound
	}
	return r, e
}
func (s *Service) Preview(ctx context.Context, p principal.Principal, id string, cursor int) (PreviewResult, error) {
	if e := p.Require("ledger:read"); e != nil {
		return PreviewResult{}, e
	}
	b, e := s.batch(ctx, p, id)
	if e != nil {
		return PreviewResult{}, e
	}
	if cursor < 0 || cursor > len(b.Candidates) {
		return PreviewResult{}, fmt.Errorf("invalid cursor")
	}
	out := PreviewResult{Proposed: []ImportRow{}, Duplicates: []ImportRow{}, Unmatched: []string{}, StatementBalance: b.ClosingBalance}
	end := min(cursor+100, len(b.Candidates))
	for _, candidate := range b.Candidates[cursor:end] {
		row, e := s.row(ctx, p, candidate.RowID)
		if e != nil {
			return out, e
		}
		if candidate.Duplicate {
			out.Duplicates = append(out.Duplicates, row)
		} else {
			out.Proposed = append(out.Proposed, row)
		}
	}
	if end < len(b.Candidates) {
		out.NextCursor = end
	}
	return out, nil
}
func (s *Service) Edit(ctx context.Context, p principal.Principal, in EditInput) (ImportRow, error) {
	if e := p.Require("import:write"); e != nil {
		return ImportRow{}, e
	}
	b, e := s.batch(ctx, p, in.BatchID)
	if e != nil {
		return ImportRow{}, e
	}
	if b.Status != "ready" {
		return ImportRow{}, fmt.Errorf("only ready batches can be edited")
	}
	owned := false
	for _, c := range b.Candidates {
		if c.RowID == in.RowID && !c.Duplicate {
			owned = true
		}
	}
	if !owned {
		return ImportRow{}, fmt.Errorf("row is a duplicate or does not belong to this batch")
	}
	old, e := s.row(ctx, p, in.RowID)
	if e != nil {
		return ImportRow{}, e
	}
	if old.EntryID != "" {
		return ImportRow{}, fmt.Errorf("row is already posted")
	}
	row := old
	row.Proposed.Postings = slices.Clone(old.Proposed.Postings)
	if in.Set.Account != nil {
		if _, e = s.ledger.Account(ctx, importWriter(p), *in.Set.Account); e != nil {
			return ImportRow{}, e
		}
		row.Proposed.Postings[1].Account = *in.Set.Account
	}
	if in.Set.Tags != nil {
		if len(*in.Set.Tags) > 50 {
			return ImportRow{}, fmt.Errorf("at most 50 tags")
		}
		row.Proposed.Tags = *in.Set.Tags
	}
	if in.Set.Skip != nil {
		row.Skip = *in.Set.Skip
	}
	if !row.Skip {
		proposal := row.Proposed
		proposal.Payee = statementPayee(proposal.Payee)
		if e = s.ledger.ValidateEntry(ctx, importWriter(p), proposal); e != nil {
			return ImportRow{}, fmt.Errorf("invalid statement proposal: %w", e)
		}
	}
	_, e = s.audit.Write(ctx, p, "import_edit", audit.Target{Collection: "import_rows", ID: row.ID}, "", old, row, func(tx context.Context) error {
		r, e := s.repo.db.Exec(tx, `UPDATE import_rows SET batch_id=@batch,account=@account,dedupe_key=@key,raw=@raw,proposed=@proposed,kind=@kind,entry_id=@entry,reversal_id=@reversal,proposed_id=@proposal,skip=@skip WHERE id=@id AND user_id=@user AND entry_id=''`, pgx.NamedArgs{"batch": row.BatchID, "account": row.Account, "key": row.DedupeKey, "raw": row.Raw, "proposed": row.Proposed, "kind": row.Kind, "entry": row.EntryID, "reversal": row.ReversalID, "proposal": row.ProposedID, "skip": row.Skip, "id": row.ID, "user": p.UserID})
		if e == nil && r.RowsAffected() != 1 {
			return principal.ErrConflict
		}
		return e
	})
	return row, e
}
func (s *Service) Commit(ctx context.Context, p principal.Principal, id string, assertClosing bool) (CommitResult, error) {
	if e := p.Require("import:write"); e != nil {
		return CommitResult{}, e
	}
	var out CommitResult
	e := s.audit.Workflow(audit.WithBatch(ctx, id), func(tx context.Context) error {
		b, e := s.batch(tx, p, id)
		if e != nil {
			return e
		}
		if b.Status != "ready" && b.Status != "committing" && b.Status != "committed" {
			return fmt.Errorf("batch is not ready to commit")
		}
		if assertClosing && b.ClosingBalance == nil {
			return fmt.Errorf("statement has no closing balance")
		}
		writer := importWriter(p)
		if _, e = s.repo.db.Exec(tx, `UPDATE import_batches SET status=@status WHERE id=@id AND user_id=@user`, pgx.NamedArgs{"status": "committing", "id": id, "user": p.UserID}); e != nil {
			return e
		}
		for _, candidate := range b.Candidates {
			if candidate.Duplicate {
				out.Skipped++
				continue
			}
			row, e := s.row(tx, p, candidate.RowID)
			if e != nil {
				return e
			}
			if row.Skip {
				out.Skipped++
				continue
			}
			if row.EntryID != "" {
				out.Skipped++
				continue
			}
			proposed := row.Proposed
			proposed.Payee = statementPayee(proposed.Payee)
			proposed.Commit = true
			entry, e := s.ledger.PostLinkedID(tx, writer, proposed, row.ProposedID, row.Kind, []ledger.Link{{Rel: "from_import_row", ID: row.ID}, {Rel: "from_import_batch", ID: id}}, "import_commit", "")
			if e != nil {
				return e
			}
			if _, e = s.repo.db.Exec(tx, `UPDATE import_rows SET entry_id=@entry WHERE id=@id AND user_id=@user`, pgx.NamedArgs{"entry": entry.Entry.ID, "id": row.ID, "user": p.UserID}); e != nil {
				return e
			}
			out.Posted++
		}
		if assertClosing {
			result, e := s.reconcile.Assert(tx, writer, reconcile.AssertInput{Account: b.Account, Date: b.To, Amount: *b.ClosingBalance, Source: "Statement " + b.FileSHA256})
			if e != nil {
				return e
			}
			out.AssertionID = result.Assertion.ID
		}
		_, e = s.audit.Write(audit.WithSummary(tx, fmt.Sprintf("Imported %d transactions from a statement, %d skipped", out.Posted, out.Skipped)), p, "import_commit", audit.Target{Collection: "import_batches", ID: id}, "", b, out, func(tx context.Context) error {
			_, e := s.repo.db.Exec(tx, `UPDATE import_batches SET status=@status WHERE id=@id AND user_id=@user`, pgx.NamedArgs{"status": "committed", "id": id, "user": p.UserID})
			return e
		})
		return e
	})
	return out, e
}
func (s *Service) Undo(ctx context.Context, p principal.Principal, id, reason string) ([]ledger.Entry, error) {
	return s.reverseBatch(ctx, p, id, reason, false)
}
func (s *Service) Redo(ctx context.Context, p principal.Principal, id, reason string) ([]ledger.Entry, error) {
	return s.reverseBatch(ctx, p, id, reason, true)
}
func (s *Service) reverseBatch(ctx context.Context, p principal.Principal, id, reason string, redo bool) ([]ledger.Entry, error) {
	if e := p.Require("import:write"); e != nil {
		return nil, e
	}
	if strings.TrimSpace(reason) == "" {
		return nil, fmt.Errorf("reason required")
	}
	out := []ledger.Entry{}
	e := s.audit.Workflow(audit.WithBatch(ctx, id+":undo"), func(tx context.Context) error {
		b, e := s.batch(tx, p, id)
		if e != nil {
			return e
		}
		start, running, finished, tool := "committed", "undoing", "undone", "import_undo"
		if redo {
			start, running, finished, tool = "undone", "reapplying", "committed", "import_redo"
		}
		if b.Status == finished {
			return nil
		}
		if b.Status != start && b.Status != running {
			return fmt.Errorf("batch must be %s before this operation", start)
		}
		_, e = s.audit.Write(tx, p, tool, audit.Target{Collection: "import_batches", ID: id}, reason, b, &out, func(tx context.Context) error {
			if _, e = s.repo.db.Exec(tx, `UPDATE import_batches SET status=@status WHERE id=@id AND user_id=@user`, pgx.NamedArgs{"status": running, "id": id, "user": p.UserID}); e != nil {
				return e
			}
			for _, c := range b.Candidates {
				if c.Duplicate {
					continue
				}
				row, e := s.row(tx, p, c.RowID)
				if e != nil {
					return e
				}
				if row.EntryID == "" {
					continue
				}
				target := row.EntryID
				if redo {
					if row.ReversalID == "" {
						continue
					}
					target = row.ReversalID
				} else if row.ReversalID != "" {
					v, e := s.ledger.Get(tx, importWriter(p), row.ReversalID)
					if e != nil {
						return e
					}
					out = append(out, v)
					continue
				}
				reversal, e := s.ledger.Reversal(tx, importWriter(p), target)
				if errors.Is(e, principal.ErrNotFound) {
					v, err := s.ledger.Reverse(tx, importWriter(p), ledger.ReverseInput{ID: target, Reason: reason})
					if err != nil {
						return err
					}
					reversal = v.Reversal
				} else if e != nil {
					return e
				}
				if _, e = s.repo.db.Exec(tx, `UPDATE import_rows SET reversal_id=CASE WHEN @redo THEN '' ELSE @reversal END,entry_id=CASE WHEN @redo THEN @reversal ELSE entry_id END WHERE id=@id AND user_id=@user`, pgx.NamedArgs{"redo": redo, "reversal": reversal.ID, "id": row.ID, "user": p.UserID}); e != nil {
					return e
				}
				out = append(out, reversal)
			}
			_, e = s.repo.db.Exec(tx, `UPDATE import_batches SET status=@status WHERE id=@id AND user_id=@user`, pgx.NamedArgs{"status": finished, "id": id, "user": p.UserID})
			return e
		})
		return e
	})
	return out, e
}
func (s *Service) SetStagedStatus(ctx context.Context, p principal.Principal, id, status, reason string) error {
	if e := p.Require("import:write"); e != nil {
		return e
	}
	if status != "ready" && status != "cancelled" {
		return fmt.Errorf("staged status must be ready or cancelled")
	}
	return s.audit.Workflow(ctx, func(tx context.Context) error {
		b, e := s.batch(tx, p, id)
		if e != nil {
			return e
		}
		if b.Status != "ready" && b.Status != "cancelled" {
			return fmt.Errorf("only uncommitted imports can change staged status")
		}
		after := b
		after.Status = status
		_, e = s.audit.Write(tx, p, "import_status", audit.Target{Collection: "import_batches", ID: id}, reason, b, after, func(tx context.Context) error {
			_, e := s.repo.db.Exec(tx, `UPDATE import_batches SET status=@status WHERE id=@id AND user_id=@user`, pgx.NamedArgs{"status": status, "id": id, "user": p.UserID})
			return e
		})
		return e
	})
}
func (s *Service) List(ctx context.Context, p principal.Principal) ([]Batch, error) {
	if e := p.Require("ledger:read"); e != nil {
		return nil, e
	}
	cur, e := s.repo.db.Query(ctx, `SELECT * FROM import_batches WHERE user_id=@user ORDER BY created_at DESC LIMIT 100`, pgx.NamedArgs{"user": p.UserID})
	if e != nil {
		return nil, e
	}
	out, e := pgx.CollectRows(cur, pgx.RowToStructByName[Batch])
	if out == nil {
		out = []Batch{}
	}
	return out, e
}

// StagedCount counts batches still awaiting or completing a commit.
func (s *Service) StagedCount(ctx context.Context, p principal.Principal) (int64, error) {
	if err := p.Require("ledger:read"); err != nil {
		return 0, err
	}
	var n int64
	err := s.repo.db.QueryRow(ctx, `SELECT count(*) FROM import_batches WHERE user_id=@user AND status IN ('staging','ready','committing')`, pgx.NamedArgs{"user": p.UserID}).Scan(&n)
	return n, err
}

// mappedAccount returns the account a CSV category names, "" for free-text categories, and an error for account-shaped values of an unknown type.
func mappedAccount(category string) (string, error) {
	prefix, _, found := strings.Cut(category, ":")
	switch {
	case !found:
		return "", nil
	case slices.Contains([]string{"expenses", "income", "assets", "liabilities", "equity"}, prefix):
		return category, nil
	}
	return "", fmt.Errorf("mapped category %q has unknown account type %q; use assets:, liabilities:, income:, expenses: or equity:", category, prefix)
}
