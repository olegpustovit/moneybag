package dashboard

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"strconv"
)

func (s *Service) Archive(ctx context.Context, p principal.Principal, id string, archived bool, reason string) error {
	if e := p.Require("dash:write"); e != nil {
		return e
	}
	return s.audit.Workflow(ctx, func(tx context.Context) error {
		var old struct {
			Archived bool `json:"archived" bson:"archived"`
		}
		args := pgx.NamedArgs{"user": p.UserID, "id": id}
		if e := s.repo.db.QueryRow(tx, `SELECT archived FROM dashboards WHERE user_id=@user AND id=@id`, args).Scan(&old.Archived); e != nil {
			if postgres.IsNotFound(e) {
				return principal.ErrNotFound
			}
			return e
		}
		next := old
		next.Archived = archived
		_, e := s.audit.Write(tx, p, "dashboard_archive", audit.Target{Collection: "dashboards", ID: id}, reason, old, next, func(tx context.Context) error {
			args["archived"] = archived
			_, e := s.repo.db.Exec(tx, `UPDATE dashboards SET archived=@archived WHERE user_id=@user AND id=@id`, args)
			return e
		})
		return e
	})
}
func (s *Service) RestorePublication(ctx context.Context, p principal.Principal, id string, old Head, reason string) error {
	if e := p.Require("dash:write"); e != nil {
		return e
	}
	if old.PublishedVersion > 0 {
		if _, e := s.Publish(ctx, p, id, old.PublishedVersion, reason); e != nil {
			return e
		}
		// Publishing the draft cleared its pointer; give the undone draft back so edits continue from it.
		h, e := s.head(ctx, p, id)
		if e != nil || h.DraftVersion != 0 || old.DraftVersion == 0 {
			return e
		}
		return s.audit.Mutation(ctx, p, func(tx context.Context) error {
			_, err := s.repo.db.Exec(tx, `UPDATE dashboards SET draft_version=@draft WHERE user_id=@user AND id=@id AND revision=@old`, pgx.NamedArgs{"draft": old.DraftVersion, "user": p.UserID, "id": id, "old": h.Revision})
			return err
		})
	}
	h, e := s.head(ctx, p, id)
	if e != nil {
		return e
	}
	draft := h.DraftVersion
	if draft == 0 {
		draft = old.DraftVersion
	}
	_, e = s.audit.Write(ctx, p, "dashboard_publish", audit.Target{Collection: "dashboards", ID: id}, reason, h, struct {
		PublishedVersion int `json:"published_version"`
	}{0}, func(tx context.Context) error {
		_, e := s.repo.db.Exec(tx, `UPDATE dashboards SET published_version=0,draft_version=@draft WHERE user_id=@user AND id=@id AND revision=@old`, pgx.NamedArgs{"draft": draft, "user": p.UserID, "id": id, "old": h.Revision})
		return e
	})
	return e
}

type VersionSummary struct {
	Version int    `json:"version"`
	Author  string `json:"author"`
	Note    string `json:"note"`
}

func (s *Service) Versions(ctx context.Context, p principal.Principal, id string) ([]VersionSummary, error) {
	if e := p.Require("ledger:read"); e != nil {
		return nil, e
	}
	if _, e := s.head(ctx, p, id); e != nil {
		return nil, e
	}
	cur, e := s.repo.db.Query(ctx, `SELECT * FROM dashboard_versions WHERE user_id=@user AND dashboard_id=@id ORDER BY version DESC LIMIT 201`, pgx.NamedArgs{"user": p.UserID, "id": id})
	if e != nil {
		return nil, e
	}
	versions, e := pgx.CollectRows(cur, pgx.RowToStructByName[Version])
	if e != nil {
		return nil, e
	}
	if len(versions) > 200 {
		return nil, fmt.Errorf("more than 200 versions; use dashboard_get with version 1..%s", strconv.Itoa(versions[0].Version))
	}
	out := []VersionSummary{}
	for _, v := range versions {
		out = append(out, VersionSummary{v.Version, v.Author.Kind + ":" + v.Author.ID, v.Note})
	}
	return out, nil
}

func (s *Service) Published(ctx context.Context, p principal.Principal, id string) (bool, error) {
	if e := p.Require("ledger:read"); e != nil {
		return false, e
	}
	h, e := s.head(ctx, p, id)
	return h.PublishedVersion > 0, e
}
