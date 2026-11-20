// Package blog is the site's blog: posts written in Markdown at
// /admin/blog by staff who may manage it, and read at /blog by anyone, with
// an Atom feed and the sitemap. A post is live once it is published and its
// published_at has passed, so publishing with a later time schedules it.
package blog

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/markdown"
	"github.com/riadafridishibly/quickstack/internal/media"
)

// Post statuses, as stored.
const (
	Draft     = "draft"
	Published = "published"
)

// States a post is in, as the admin list shows them.
const (
	stateDraft     = "draft"
	stateScheduled = "scheduled"
	statePublished = "published"
)

func state(status string, publishedAt *time.Time, now time.Time) string {
	switch {
	case status != Published:
		return stateDraft
	case publishedAt.After(now):
		return stateScheduled
	}
	return statePublished
}

func stateLabel(s string) string {
	switch s {
	case stateDraft:
		return "Draft"
	case stateScheduled:
		return "Scheduled"
	case statePublished:
		return "Published"
	}
	return s
}

// live reports whether anyone may read p.
func live(p db.Post, now time.Time) bool {
	return state(p.Status, p.PublishedAt, now) == statePublished
}

var (
	errInvalid  = errors.New("blog: invalid post")
	errConflict = errors.New("blog: post changed")
)

// NewPost is a post for Create, with the fields of the admin form.
type NewPost struct {
	Title, Slug, Summary, Body string
	// Cover is an image's address in Media, such as /media/ab12cd34.
	Cover string
	Tags  []string
	// Publish publishes the post at PublishedAt, or now if it is nil.
	// Otherwise the post is a draft.
	Publish     bool
	PublishedAt *time.Time
}

// Create validates p and saves it as a new post, as part of tx, as the
// admin form does.
func Create(ctx context.Context, tx pgx.Tx, actorID int64, p NewPost) (db.Post, error) {
	v := url.Values{
		"title": {p.Title}, "slug": {p.Slug}, "summary": {p.Summary}, "body": {p.Body},
		"cover": {p.Cover}, "tags": {strings.Join(p.Tags, ",")}, "action": {actionSave},
	}
	if p.Publish {
		v.Set("action", actionPublish)
	}
	if p.PublishedAt != nil {
		v.Set("published_at", p.PublishedAt.UTC().Format(publishAtLayout))
	}
	f := postFormOfValues(v.Get)
	if !f.validate() {
		return db.Post{}, fmt.Errorf("%w: %v", errInvalid, f.Errors)
	}
	post, err := save(ctx, tx, actorID, nil, &f)
	if errors.Is(err, errInvalid) {
		return db.Post{}, fmt.Errorf("%w: %v", errInvalid, f.Errors)
	}
	return post, err
}

// save writes the validated form f as part of tx: a new post when old is
// nil, a change to old otherwise. It returns errInvalid, with f.Errors set,
// for a cover that isn't in Media or a slug another post has.
func save(ctx context.Context, tx pgx.Tx, actorID int64, old *db.Post, f *postForm) (db.Post, error) {
	q := db.New(tx)
	var coverID *int64
	if f.coverKey != "" {
		a, err := q.GetAssetByKey(ctx, f.coverKey)
		if errors.Is(err, pgx.ErrNoRows) {
			f.Errors["cover"] = "There is no image " + f.Cover + " in Media."
			return db.Post{}, errInvalid
		}
		if err != nil {
			return db.Post{}, err
		}
		if media.IsSVG(a) && !media.SVGRaster {
			f.Errors["cover"] = "A cover can’t be an SVG: it is also the picture shown when the post is shared, and social sites don’t show SVG. Use a JPEG, PNG, WebP, GIF or HEIC image."
			return db.Post{}, errInvalid
		}
		coverID = &a.ID
	}

	status, publishedAt := Draft, f.publishAt
	if old != nil {
		status = old.Status
	}
	switch f.Action {
	case actionPublish:
		status = Published
		if publishedAt == nil {
			now := time.Now().UTC().Truncate(time.Second)
			publishedAt = &now
		}
	case actionUnpublish:
		status = Draft
	}
	if status == Published && publishedAt == nil {
		f.Errors["published_at"] = "Enter when the post is published, or unpublish it."
		return db.Post{}, errInvalid
	}

	// Rendering finds the images the body shows.
	var assetIDs []int64
	_, _, err := markdown.Render(ctx, f.Body, func(ctx context.Context, keys []string) ([]db.Asset, error) {
		assets, err := q.ListAssetsByKeys(ctx, keys)
		for _, a := range assets {
			assetIDs = append(assetIDs, a.ID)
		}
		return assets, err
	})
	if err != nil {
		return db.Post{}, err
	}

	var p db.Post
	if old == nil {
		p, err = q.CreatePost(ctx, db.CreatePostParams{
			Slug: f.Slug, Title: f.Title, Summary: f.Summary, Body: f.Body, CoverID: coverID,
			AuthorID: &actorID, Status: status, PublishedAt: publishedAt, Tags: f.tags,
		})
	} else {
		p, err = q.UpdatePost(ctx, db.UpdatePostParams{
			ID: old.ID, Slug: f.Slug, Title: f.Title, Summary: f.Summary, Body: f.Body, CoverID: coverID,
			Status: status, PublishedAt: publishedAt, Tags: f.tags,
		})
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "23505" {
		f.Errors["slug"] = "Another post has this slug."
		return db.Post{}, errInvalid
	}
	if err != nil {
		return db.Post{}, err
	}

	if err := q.ClearPostMedia(ctx, p.ID); err != nil {
		return p, err
	}
	if len(assetIDs) > 0 {
		slices.Sort(assetIDs)
		if err := q.AddPostMedia(ctx, db.AddPostMediaParams{PostID: p.ID, AssetIds: slices.Compact(assetIDs)}); err != nil {
			return p, err
		}
	}

	// A post's own slug wins over a redirect from it.
	if err := q.DeletePostRedirect(ctx, p.Slug); err != nil {
		return p, err
	}
	audit := func(action, target string) error {
		return q.RecordAudit(ctx, db.RecordAuditParams{ActorID: &actorID, Action: action, Target: target})
	}
	wasPublished := old != nil && old.Status == Published
	switch {
	case status == Published && !wasPublished:
		err = audit("blog.published", p.Slug)
	case status == Draft && wasPublished:
		err = audit("blog.unpublished", p.Slug)
	}
	if err != nil {
		return p, err
	}
	if old != nil && old.Slug != p.Slug {
		if wasPublished {
			if err := q.AddPostRedirect(ctx, db.AddPostRedirectParams{OldSlug: old.Slug, PostID: p.ID}); err != nil {
				return p, err
			}
		}
		if err := audit("blog.slug_changed", old.Slug+" → "+p.Slug); err != nil {
			return p, err
		}
	}
	return p, nil
}
