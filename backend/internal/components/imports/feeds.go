package imports

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/safefetch"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Feed struct {
	Key            string `json:"-" bson:"_id" db:"storage_key"`
	UserID         string `json:"-" bson:"user_id" db:"user_id"`
	ID             string `json:"id" bson:"id" db:"id"`
	URL            string `json:"-" bson:"url" db:"url"`
	Account        string `json:"account" bson:"account" db:"account"`
	Format         string `json:"format" bson:"format" db:"format"`
	DateFormat     string `json:"date_format,omitempty" bson:"date_format,omitempty" db:"date_format"`
	TokenEncrypted string `json:"-" bson:"token_encrypted" db:"token_encrypted"`
}
type FeedInput struct {
	ID         string
	URL        string
	Account    string
	Format     string
	DateFormat string
	Token      string
}

func (s *Service) SetSecretKey(key []byte) { s.secret = append([]byte{}, key...) }

// SetHTTPClient replaces the public-only statement client; tests use it to serve statements over local TLS.
func (s *Service) SetHTTPClient(c *http.Client) { s.http = c }
func (s *Service) seal(owner, id, raw string) (string, error) {
	if len(s.secret) != 32 {
		return "", fmt.Errorf("configure MONEYBAG_SECRET_KEY before connecting a feed")
	}
	block, e := aes.NewCipher(s.secret)
	if e != nil {
		return "", e
	}
	aead, e := cipher.NewGCM(block)
	if e != nil {
		return "", e
	}
	nonce := make([]byte, aead.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return "", e
	}
	return base64.RawURLEncoding.EncodeToString(aead.Seal(nonce, nonce, []byte(raw), []byte(owner+":"+id))), nil
}
func (s *Service) open(owner, id, encoded string) (string, error) {
	if len(s.secret) != 32 {
		return "", fmt.Errorf("feed secret key is unavailable")
	}
	block, e := aes.NewCipher(s.secret)
	if e != nil {
		return "", e
	}
	aead, e := cipher.NewGCM(block)
	if e != nil {
		return "", e
	}
	b, e := base64.RawURLEncoding.DecodeString(encoded)
	if e != nil || len(b) < aead.NonceSize() {
		return "", fmt.Errorf("invalid encrypted feed credential")
	}
	plain, e := aead.Open(nil, b[:aead.NonceSize()], b[aead.NonceSize():], []byte(owner+":"+id))
	if e != nil {
		return "", fmt.Errorf("could not decrypt feed credential")
	}
	return string(plain), nil
}
func (s *Service) ConfigureFeed(ctx context.Context, p principal.Principal, in FeedInput) (Feed, error) {
	if e := p.Require("import:write"); e != nil {
		return Feed{}, e
	}
	if in.ID != "wise" && in.ID != "finance_connector" {
		return Feed{}, fmt.Errorf("feed must be wise or finance_connector")
	}
	u, e := url.Parse(in.URL)
	if e != nil || u.Scheme != "https" || u.User != nil || u.Hostname() == "" || u.Fragment != "" || u.Port() != "" && u.Port() != "443" {
		return Feed{}, fmt.Errorf("feed URL must be public HTTPS")
	}
	if in.Format != "wise_csv" && in.Format != "generic_csv" && in.Format != "plaid" {
		return Feed{}, fmt.Errorf("feed format must be wise_csv, generic_csv or plaid")
	}
	if in.DateFormat != "" && in.DateFormat != "iso" && in.DateFormat != "dmy" && in.DateFormat != "mdy" {
		return Feed{}, fmt.Errorf("feed date format must be iso, dmy or mdy")
	}
	if len(in.Token) > 8192 || strings.ContainsAny(in.Token, "\r\n") {
		return Feed{}, fmt.Errorf("invalid feed credential")
	}
	if _, e = s.ledger.Account(ctx, importWriter(p), in.Account); e != nil {
		return Feed{}, e
	}
	old, e := s.repo.feed(ctx, p.UserID, in.ID)
	if e != nil && !postgres.IsNotFound(e) {
		return Feed{}, e
	}
	encrypted := old.TokenEncrypted
	if in.Token != "" || encrypted == "" {
		encrypted, e = s.seal(p.UserID, in.ID, in.Token)
		if e != nil {
			return Feed{}, e
		}
	}
	feed := Feed{Key: p.UserID + ":" + in.ID, UserID: p.UserID, ID: in.ID, URL: in.URL, Account: in.Account, Format: in.Format, DateFormat: in.DateFormat, TokenEncrypted: encrypted}
	_, e = s.audit.Write(ctx, p, "feed_configure", audit.Target{Collection: "feeds", ID: in.ID}, "", old, feed, func(tx context.Context) error {
		_, e := s.repo.db.Exec(tx, `INSERT INTO feeds(storage_key,user_id,id,url,account,format,date_format,token_encrypted) VALUES (@key,@user,@id,@url,@account,@format,@date,@token) ON CONFLICT(storage_key) DO UPDATE SET id=EXCLUDED.id,url=EXCLUDED.url,account=EXCLUDED.account,format=EXCLUDED.format,date_format=EXCLUDED.date_format,token_encrypted=EXCLUDED.token_encrypted WHERE feeds.user_id=EXCLUDED.user_id`, pgx.NamedArgs{"key": feed.Key, "user": p.UserID, "id": feed.ID, "url": feed.URL, "account": feed.Account, "format": feed.Format, "date": feed.DateFormat, "token": feed.TokenEncrypted})
		return e
	})
	return feed, e
}

// HasFeedSecret says whether a person has entered a credential, without ever returning it.
func (s *Service) HasFeedSecret(p principal.Principal, feed Feed) bool {
	token, e := s.open(p.UserID, feed.ID, feed.TokenEncrypted)
	return e == nil && token != ""
}

// SetFeedSecret stores a credential entered on the secure page, for a feed an agent already set up.
func (s *Service) SetFeedSecret(ctx context.Context, p principal.Principal, id, token string) error {
	if e := p.Require("import:write"); e != nil {
		return e
	}
	if token == "" {
		return fmt.Errorf("enter the credential")
	}
	feed, e := s.repo.feed(ctx, p.UserID, id)
	if postgres.IsNotFound(e) {
		return principal.ErrNotFound
	}
	if e != nil {
		return e
	}
	_, e = s.ConfigureFeed(ctx, p, FeedInput{ID: id, URL: feed.URL, Account: feed.Account, Format: feed.Format, DateFormat: feed.DateFormat, Token: token})
	return e
}

func (s *Service) Sync(ctx context.Context, p principal.Principal, id, since string) (StartResult, error) {
	if e := p.Require("import:write"); e != nil {
		return StartResult{}, e
	}
	if id != "wise" && id != "finance_connector" {
		return StartResult{}, fmt.Errorf("feed must be wise or finance_connector")
	}
	if since != "" {
		if _, e := time.Parse("2006-01-02", since); e != nil {
			if d, e := time.Parse(time.RFC3339, since); e == nil {
				since = d.Format("2006-01-02")
			} else {
				return StartResult{}, fmt.Errorf("since must be YYYY-MM-DD or RFC3339")
			}
		}
	}
	feed, e := s.repo.feed(ctx, p.UserID, id)
	if postgres.IsNotFound(e) {
		return StartResult{}, fmt.Errorf("connect the %s feed in Settings first", id)
	}
	if e != nil {
		return StartResult{}, e
	}
	token, e := s.open(p.UserID, id, feed.TokenEncrypted)
	if e != nil {
		return StartResult{}, e
	}
	content, _, e := safefetch.ReadBearer(ctx, s.http, feed.URL, 8*1024*1024, token)
	if e != nil {
		return StartResult{}, fmt.Errorf("feed download failed; check its URL and credential in Settings")
	}
	return s.start(ctx, p, StartInput{Account: feed.Account, Format: feed.Format, Mapping: Mapping{DateFormat: feed.DateFormat}, ContentB64: base64.StdEncoding.EncodeToString(content)}, since)
}

func (s *Service) Feeds(ctx context.Context, p principal.Principal) ([]Feed, error) {
	if e := p.Require("ledger:read"); e != nil {
		return nil, e
	}
	cur, e := s.repo.db.Query(ctx, `SELECT * FROM feeds WHERE user_id=@user LIMIT 3`, pgx.NamedArgs{"user": p.UserID})
	if e != nil {
		return nil, e
	}
	out, e := pgx.CollectRows(cur, pgx.RowToStructByName[Feed])
	if out == nil {
		out = []Feed{}
	}
	return out, e
}
