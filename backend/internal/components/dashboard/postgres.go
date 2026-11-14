package dashboard

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (r *PostgresRepository) head(ctx context.Context, user, id string) (Head, error) {
	rows, err := r.db.Query(ctx, `SELECT * FROM dashboards WHERE user_id=@user AND id=@id AND NOT archived`, pgx.NamedArgs{"user": user, "id": id})
	if err != nil {
		return Head{}, err
	}
	return pgx.CollectOneRow(rows, pgx.RowToStructByName[Head])
}
func (r *PostgresRepository) version(ctx context.Context, user, id string, n int) (Version, error) {
	rows, err := r.db.Query(ctx, `SELECT * FROM dashboard_versions WHERE user_id=@user AND dashboard_id=@id AND version=@version`, pgx.NamedArgs{"user": user, "id": id, "version": n})
	if err != nil {
		return Version{}, err
	}
	return pgx.CollectOneRow(rows, pgx.RowToStructByName[Version])
}
func (r *PostgresRepository) insertHead(ctx context.Context, h Head) (pgconn.CommandTag, error) {
	return r.db.Exec(ctx, `INSERT INTO dashboards(storage_key,user_id,id,title,published_version,draft_version,revision,home,archived,published_by,published_at,publish_note) VALUES (@key,@user,@id,@title,@published,@draft,@revision,@home,@archived,@actor,@at,@note)`, pgx.NamedArgs{"key": h.Key, "user": h.UserID, "id": h.ID, "title": h.Title, "published": h.PublishedVersion, "draft": h.DraftVersion, "revision": h.Revision, "home": h.Home, "archived": h.Archived, "actor": h.PublishedBy, "at": h.PublishedAt, "note": h.PublishNote})
}
func (r *PostgresRepository) insertVersion(ctx context.Context, v Version) (pgconn.CommandTag, error) {
	return r.db.Exec(ctx, `INSERT INTO dashboard_versions(storage_key,user_id,dashboard_id,version,spec,author,created_at,note) VALUES (@key,@user,@id,@version,@spec,@author,@at,@note)`, pgx.NamedArgs{"key": v.Key, "user": v.UserID, "id": v.DashboardID, "version": v.Version, "spec": v.Spec, "author": v.Author, "at": v.CreatedAt, "note": v.Note})
}
