package dashboard

import (
	"context"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
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
		filter := bson.M{"user_id": p.UserID, "id": id}
		if e := s.repo.db.Collection("dashboards").FindOne(tx, filter).Decode(&old); e != nil {
			return e
		}
		next := old
		next.Archived = archived
		_, e := s.audit.Write(tx, p, "dashboard_archive", audit.Target{Collection: "dashboards", ID: id}, reason, old, next, func(tx context.Context) error {
			_, e := s.repo.db.Collection("dashboards").UpdateOne(tx, filter, bson.M{"$set": bson.M{"archived": archived}})
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
		return nil
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
		_, e := s.repo.db.Collection("dashboards").UpdateOne(tx, bson.M{"user_id": p.UserID, "id": id, "revision": h.Revision}, bson.M{"$set": bson.M{"published_version": 0, "draft_version": draft}})
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
	cur, e := s.repo.db.Collection("dashboard_versions").Find(ctx, bson.M{"user_id": p.UserID, "dashboard_id": id}, options.Find().SetSort(bson.D{{Key: "version", Value: -1}}).SetLimit(201))
	if e != nil {
		return nil, e
	}
	defer cur.Close(ctx)
	var versions []Version
	if e = cur.All(ctx, &versions); e != nil {
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
