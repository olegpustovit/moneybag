package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/reqcache"
	"regexp"
	"slices"
	"strings"
)

type PostgresRepository struct{ DB *pgxpool.Pool }

func NewPostgresRepository(db *pgxpool.Pool) *PostgresRepository { return &PostgresRepository{db} }
func (r *PostgresRepository) db(p principal.Principal) (*pgxpool.Pool, error) {
	if err := p.Valid(); err != nil {
		return nil, err
	}
	return r.DB, nil
}
func (r *PostgresRepository) Account(ctx context.Context, p principal.Principal, id string) (Account, error) {
	db, e := r.db(p)
	if e != nil {
		return Account{}, e
	}
	if all, ok, e := CachedAccounts(ctx, db, p); ok {
		if e != nil {
			return Account{}, e
		}
		i, found := slices.BinarySearchFunc(all, id, func(a Account, id string) int { return strings.Compare(a.ID, id) })
		if !found {
			return Account{}, principal.ErrNotFound
		}
		return all[i], nil
	}
	rows, e := db.Query(ctx, `SELECT * FROM accounts WHERE user_id=@user AND id=@id`, pgx.NamedArgs{"user": p.UserID, "id": id})
	if e != nil {
		return Account{}, e
	}
	a, e := pgx.CollectOneRow(rows, pgx.RowToStructByName[Account])
	if errors.Is(e, pgx.ErrNoRows) {
		e = principal.ErrNotFound
	}
	return a, e
}
func (r *PostgresRepository) Entry(ctx context.Context, p principal.Principal, id string) (Entry, error) {
	db, e := r.db(p)
	if e != nil {
		return Entry{}, e
	}
	rows, e := db.Query(ctx, `SELECT j.user_id,j.id,j.seq,j.date,j.kind,j.payee,j.postings,CASE WHEN t.id IS NULL THEN j.tags ELSE t.tags END AS tags,j.links,j.status,j.actor,j.created_at FROM journal j `+entryTagJoin+` WHERE j.user_id=@user AND j.id=@id`, pgx.NamedArgs{"user": p.UserID, "id": id})
	if e != nil {
		return Entry{}, e
	}
	entry, e := pgx.CollectOneRow(rows, pgx.RowToStructByName[Entry])
	if errors.Is(e, pgx.ErrNoRows) {
		e = principal.ErrNotFound
	}
	return entry, e
}

const entryColumns = `j.user_id,j.id,j.seq,j.date,j.kind,j.payee,j.postings,j.tags,j.links,j.status,j.actor,j.created_at`
const overlaidEntryColumns = `j.user_id,j.id,j.seq,j.date,j.kind,j.payee,j.postings,COALESCE(t.tags,j.tags) AS tags,j.links,j.status,j.actor,j.created_at`
const entryTagJoin = `LEFT JOIN entry_tags t ON t.id=j.user_id||':'||j.id AND t.user_id=j.user_id`
const insertEntrySQL = `INSERT INTO journal (user_id,id,seq,date,kind,payee,postings,tags,links,status,actor,created_at)
 VALUES (@user,@id,@seq,@date,@kind,@payee,COALESCE(@postings::jsonb,'null'::jsonb),@tags,COALESCE(@links::jsonb,'null'::jsonb),@status,@actor,@created)`

func (r *PostgresRepository) insert(ctx context.Context, p principal.Principal, e *Entry) error {
	db, err := r.db(p)
	if err != nil {
		return err
	}
	e.Seq, err = postgres.Next(ctx, db, p.UserID+":journal")
	if err != nil {
		return err
	}
	if _, err = db.Exec(ctx, insertEntrySQL, pgx.NamedArgs{"user": e.UserID, "id": e.ID, "seq": e.Seq, "date": e.Date, "kind": e.Kind, "payee": e.Payee, "postings": e.Postings, "tags": e.Tags, "links": e.Links, "status": e.Status, "actor": e.Actor, "created": e.CreatedAt}); err != nil {
		return err
	}
	_, err = db.Exec(ctx, `UPDATE balances SET dirty=true WHERE user_id=@user`, pgx.NamedArgs{"user": p.UserID})
	return err
}
func (r *PostgresRepository) Entries(ctx context.Context, p principal.Principal, in EntriesInput) (EntriesPage, error) {
	db, e := r.db(p)
	if e != nil {
		return EntriesPage{}, e
	}
	excluded := []string{}
	if in.Effective {
		reversed, e := r.reversed(ctx, p)
		if e != nil {
			return EntriesPage{}, e
		}
		for id := range reversed {
			excluded = append(excluded, id)
		}
	}
	limit := in.Limit
	if limit == 0 {
		limit = 100
	}
	var cursorAmount *Decimal
	if in.CursorAmount != "" && in.CursorAmount != "null" {
		value, _ := ParseDecimal(in.CursorAmount)
		cursorAmount = &value
	}
	const entriesSQL = `WITH filtered AS (
 SELECT j.*,COALESCE(t.tags,j.tags) AS current_tags,
 CASE WHEN @sort IN ('amount','-amount') THEN (SELECT (p->'amount'->>'v')::numeric FROM jsonb_array_elements(COALESCE(NULLIF(j.postings,'null'::jsonb),'[]'::jsonb)) p
  WHERE (@account='' AND cardinality(@accounts::text[])=0) OR p->>'account'=@account OR p->>'account'=ANY(@accounts::text[]) LIMIT 1) END AS sort_amount
 FROM journal j ` + entryTagJoin + ` WHERE j.user_id=@user AND (@from='' OR j.date>=@from) AND (@to='' OR j.date<=@to)
 AND (@kind='' OR j.kind=@kind) AND (@status='' OR j.status=@status)
 AND (@account='' OR EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(NULLIF(j.postings,'null'::jsonb),'[]'::jsonb)) p WHERE p->>'account'=@account))
 AND (cardinality(@accounts::text[])=0 OR EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(NULLIF(j.postings,'null'::jsonb),'[]'::jsonb)) p WHERE p->>'account'=ANY(@accounts::text[])))
 AND (NOT @effective OR (NOT (j.id=ANY(@excluded::text[])) AND (@kind<>'' OR j.kind IS DISTINCT FROM 'reversal')))
 AND (@matches::text[] IS NULL OR ((j.id=ANY(@matches::text[]))<>@exclude_matches))
 AND (@text='' OR j.payee COLLATE "pg_c_utf8" ~* @text)
 AND (@tag='' OR @tag=ANY(COALESCE(t.tags,j.tags)))
 ) SELECT j.user_id,j.id,j.seq,j.date,j.kind,j.payee,j.postings,j.current_tags AS tags,j.links,j.status,j.actor,j.created_at FROM filtered j
 WHERE @cursor::bigint<=0 OR
 (@sort='' AND seq<@cursor) OR
 (@sort='date' AND (date,seq)>(@cursor_date,@cursor)) OR
 (@sort='-date' AND (date,seq)<(@cursor_date,@cursor)) OR
 (@sort='amount' AND ((@cursor_amount::numeric IS NULL AND (sort_amount IS NOT NULL OR (sort_amount IS NULL AND seq>@cursor)))
 OR (@cursor_amount::numeric IS NOT NULL AND (sort_amount>@cursor_amount OR (sort_amount=@cursor_amount AND seq>@cursor))))) OR
 (@sort='-amount' AND ((@cursor_amount::numeric IS NULL AND sort_amount IS NULL AND seq<@cursor)
 OR (@cursor_amount::numeric IS NOT NULL AND (sort_amount IS NULL OR sort_amount<@cursor_amount OR (sort_amount=@cursor_amount AND seq<@cursor)))))
 ORDER BY CASE WHEN @sort='date' THEN date END ASC, CASE WHEN @sort='-date' THEN date END DESC,
 CASE WHEN @sort='amount' THEN sort_amount END ASC NULLS FIRST, CASE WHEN @sort='-amount' THEN sort_amount END DESC NULLS LAST,
 CASE WHEN @sort IN ('date','amount') THEN seq END ASC, CASE WHEN @sort NOT IN ('date','amount') THEN seq END DESC LIMIT @limit`
	accounts := in.Accounts
	if accounts == nil {
		accounts = []string{}
	}
	rows, e := db.Query(ctx, entriesSQL, pgx.NamedArgs{"user": p.UserID, "from": in.From, "to": in.To, "kind": in.Kind, "status": in.Status, "account": in.Account, "accounts": accounts, "effective": in.Effective, "excluded": excluded, "matches": in.MatchIDs, "exclude_matches": in.ExcludeIDs, "text": regexp.QuoteMeta(in.Text), "tag": in.Tag, "cursor": in.Cursor, "sort": in.Sort, "cursor_date": in.CursorDate, "cursor_amount": cursorAmount, "limit": limit + 1})
	if e != nil {
		return EntriesPage{}, e
	}
	entries, e := pgx.CollectRows(rows, pgx.RowToStructByName[Entry])
	out := EntriesPage{Items: entries}
	amountSort := in.Sort == "amount" || in.Sort == "-amount"
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		out.NextCursor = out.Items[limit-1].Seq
		if in.Sort == "date" || in.Sort == "-date" {
			out.NextCursorDate = out.Items[limit-1].Date
		}
		if amountSort {
			out.NextCursorAmount = "null"
			for _, posting := range out.Items[limit-1].Postings {
				if (in.Account == "" && len(in.Accounts) == 0) || posting.Account == in.Account || slices.Contains(in.Accounts, posting.Account) {
					out.NextCursorAmount = posting.Amount.Value.String()
					break
				}
			}
		}
	}
	return out, e
}

// reversed returns original entries cancelled by an odd reversal chain.
func (r *PostgresRepository) reversed(ctx context.Context, p principal.Principal) (map[string]bool, error) {
	return reqcache.Do(ctx, "reversed|"+p.UserID, func() (map[string]bool, error) {
		if dates, ok := combinedDates(ctx); ok {
			combined, err := r.combinedJournal(ctx, p, dates)
			if err == nil {
				var revs []Entry
				for i, doc := range combined.entries {
					if i%1000 == 0 && ctx.Err() != nil {
						return nil, ctx.Err()
					}
					var item struct {
						ID    string          `json:"id"`
						Kind  string          `json:"kind"`
						Links json.RawMessage `json:"links"`
					}
					if err := json.Unmarshal(doc, &item); err != nil {
						return nil, err
					}
					if item.Kind != "reversal" {
						continue
					}
					var links []Link
					if len(item.Links) > 0 {
						if err := json.Unmarshal(item.Links, &links); err != nil {
							return nil, err
						}
					}
					revs = append(revs, Entry{ID: item.ID, Links: links})
				}
				if result, unique := reversalSet(revs); unique {
					return result, nil
				}
			} else if ctx.Err() != nil {
				return nil, err
			}
		}
		db, err := r.db(p)
		if err != nil {
			return nil, err
		}
		rows, err := db.Query(ctx, `SELECT id,links FROM journal WHERE user_id=@user AND kind='reversal' AND status='posted'`, pgx.NamedArgs{"user": p.UserID})
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var revs []Entry
		for rows.Next() {
			var rev Entry
			if err := rows.Scan(&rev.ID, &rev.Links); err != nil {
				return nil, err
			}
			revs = append(revs, rev)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		result, _ := reversalSet(revs)
		return result, nil
	})
}

func reversalSet(revs []Entry) (map[string]bool, bool) {
	reversedBy, isReversal := map[string]string{}, map[string]bool{}
	unique := true
	for _, r := range revs {
		isReversal[r.ID] = true
		for _, l := range r.Links {
			if l.Rel == "reverses" {
				if prior, exists := reversedBy[l.ID]; exists && prior != r.ID {
					unique = false
				}
				reversedBy[l.ID] = r.ID
			}
		}
	}
	reversed := map[string]bool{}
	for id := range reversedBy {
		if isReversal[id] {
			continue
		}
		n := 0
		for cur, ok := reversedBy[id]; ok && n <= len(revs); cur, ok = reversedBy[cur] {
			n++
		}
		if n%2 == 1 {
			reversed[id] = true
		}
	}
	return reversed, unique
}

type sharedPostedKey struct{}

// WithSharedPostedStream allows identical journal sources to reuse bounded raw documents for one read request.
func WithSharedPostedStream(ctx context.Context) context.Context {
	return context.WithValue(ctx, sharedPostedKey{}, true)
}

// PostedStream reads posted journal entries without paging or joining tags.
func (r *PostgresRepository) PostedStream(ctx context.Context, p principal.Principal, from, to string, fn func(Entry) error) error {
	if dates, ok := combinedDates(ctx); ok {
		combined, err := r.combinedJournal(ctx, p, dates)
		if err == nil {
			return streamPostedRaw(ctx, combined.entries, p.UserID, from, to, fn)
		}
		if ctx.Err() != nil {
			return err
		}
	}
	if shared, _ := ctx.Value(sharedPostedKey{}).(bool); shared {
		db, err := r.db(p)
		if err != nil {
			return err
		}
		key := fmt.Sprintf("posted-raw|%p|%d:%s", db, len(p.UserID), p.UserID)
		raw, err := reqcache.Do(ctx, key, func() ([]json.RawMessage, error) {
			cur, err := r.postedCursor(ctx, p, "", "")
			if err != nil {
				return nil, err
			}
			defer cur.Close()
			var rows []json.RawMessage
			size := 0
			for cur.Next() {
				var doc json.RawMessage
				if err := cur.Scan(&doc); err != nil {
					return nil, err
				}
				size += len(doc)
				// shortcut: cache at most 32 MiB or 250,000 entries; fall back to streaming for larger journals.
				if size > 32<<20 || len(rows) >= 250000 {
					return nil, errors.New("shared journal exceeds cache limit")
				}
				rows = append(rows, append(json.RawMessage(nil), doc...))
			}
			return rows, cur.Err()
		})
		if err == nil {
			return streamPostedRaw(ctx, raw, p.UserID, from, to, fn)
		}
		if ctx.Err() != nil {
			return err
		}
	}
	cur, err := r.postedCursor(ctx, p, from, to)
	if err != nil {
		return err
	}
	defer cur.Close()
	for cur.Next() {
		var doc json.RawMessage
		if err := cur.Scan(&doc); err != nil {
			return err
		}
		var entry Entry
		if err := json.Unmarshal(doc, &entry); err != nil {
			return err
		}
		entry.UserID = p.UserID
		if err := fn(entry); err != nil {
			return err
		}
	}
	return cur.Err()
}

func streamPostedRaw(ctx context.Context, raw []json.RawMessage, user, from, to string, fn func(Entry) error) error {
	for _, doc := range raw {
		if err := ctx.Err(); err != nil {
			return err
		}
		if from != "" || to != "" {
			var header struct {
				Date string `json:"date"`
			}
			if err := json.Unmarshal(doc, &header); err != nil {
				return err
			}
			date := header.Date
			if from != "" && date < from || to != "" && date > to {
				continue
			}
		}
		var entry Entry
		if err := json.Unmarshal(doc, &entry); err != nil {
			return err
		}
		entry.UserID = user
		if err := fn(entry); err != nil {
			return err
		}
	}
	return nil
}

func (r *PostgresRepository) postedCursor(ctx context.Context, p principal.Principal, from, to string) (pgx.Rows, error) {
	db, err := r.db(p)
	if err != nil {
		return nil, err
	}
	const postedSQL = `SELECT to_jsonb(j) FROM journal j WHERE user_id=@user AND status='posted' AND (@from='' OR date>=@from) AND (@to='' OR date<=@to) ORDER BY seq`
	return db.Query(ctx, postedSQL, pgx.NamedArgs{"user": p.UserID, "from": from, "to": to})
}

func (r *PostgresRepository) tagOverlays(ctx context.Context, p principal.Principal) (map[string][]string, error) {
	return reqcache.Do(ctx, "entry-tags|"+p.UserID, func() (map[string][]string, error) {
		db, err := r.db(p)
		if err != nil {
			return nil, err
		}
		rows, err := db.Query(ctx, `SELECT entry_id,tags FROM entry_tags WHERE user_id=@user`, pgx.NamedArgs{"user": p.UserID})
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		tags := map[string][]string{}
		for rows.Next() {
			var id string
			var value []string
			if err := rows.Scan(&id, &value); err != nil {
				return nil, err
			}
			tags[id] = value
		}
		return tags, rows.Err()
	})
}

func (r *PostgresRepository) Accounts(ctx context.Context, p principal.Principal, in AccountsInput) (AccountsPage, error) {
	db, e := r.db(p)
	if e != nil {
		return AccountsPage{}, e
	}
	limit := in.Limit
	if limit == 0 {
		limit = 100
	}
	if all, ok, e := CachedAccounts(ctx, db, p); ok {
		out := AccountsPage{Items: []Account{}}
		for _, a := range all {
			if e != nil || !in.IncludeClosed && a.ClosedAt != nil || in.Type != "" && a.Type != in.Type || in.Pot != "" && a.Pot != in.Pot || a.ID <= in.Cursor {
				continue
			}
			if len(out.Items) == limit {
				out.NextCursor = out.Items[limit-1].ID
				break
			}
			out.Items = append(out.Items, a)
		}
		return out, e
	}
	const accountsSQL = `SELECT * FROM accounts WHERE user_id=@user AND (@closed OR closed_at IS NULL)
 AND (@type='' OR type=@type) AND (@pot='' OR pot=@pot) AND (@cursor='' OR id>@cursor) ORDER BY id LIMIT @limit`
	rows, e := db.Query(ctx, accountsSQL, pgx.NamedArgs{"user": p.UserID, "closed": in.IncludeClosed, "type": in.Type, "pot": in.Pot, "cursor": in.Cursor, "limit": limit + 1})
	if e != nil {
		return AccountsPage{}, e
	}
	accounts, e := pgx.CollectRows(rows, pgx.RowToStructByName[Account])
	out := AccountsPage{Items: accounts}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		out.NextCursor = out.Items[limit-1].ID
	}
	return out, e
}
func (r *PostgresRepository) Init(ctx context.Context, p principal.Principal) error { return p.Valid() }
