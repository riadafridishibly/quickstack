package blog

import (
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/media"
	"github.com/riadafridishibly/quickstack/internal/slug"
)

const (
	maxSlugLen    = 80
	maxTitleLen   = 200
	maxSummaryLen = 500
	maxBodyLen    = 200_000
	maxTags       = 10

	// publishAtLayout is what <input type="datetime-local"> sends. The time
	// is UTC.
	publishAtLayout = "2006-01-02T15:04"
)

// The buttons that submit the post form.
const (
	actionSave      = "save"
	actionPublish   = "publish"
	actionUnpublish = "unpublish"
)

type postForm struct {
	Title     string
	Slug      string
	Summary   string
	Body      string
	Cover     string
	Tags      string
	PublishAt string
	// UpdatedAt is when the post was last saved as the editor opened it.
	UpdatedAt string
	Action    string
	Errors    map[string]string
	// Conflict says someone else saved the post since the form was opened.
	Conflict bool

	// Set by validate.
	coverKey  string
	tags      []string
	publishAt *time.Time
	updatedAt time.Time
}

func postFormFrom(r *http.Request) postForm { return postFormOfValues(r.PostFormValue) }

// postFormOfValues reads the form's fields with get.
func postFormOfValues(get func(name string) string) postForm {
	f := postForm{
		Title:     strings.Join(strings.Fields(get("title")), " "),
		Slug:      strings.TrimSpace(get("slug")),
		Summary:   strings.Join(strings.Fields(get("summary")), " "),
		Body:      strings.TrimSpace(strings.ReplaceAll(get("body"), "\r\n", "\n")),
		Cover:     strings.TrimSpace(get("cover")),
		Tags:      strings.TrimSpace(get("tags")),
		PublishAt: strings.TrimSpace(get("published_at")),
		UpdatedAt: get("updated_at"),
		Action:    get("action"),
		Errors:    map[string]string{},
	}
	if f.Action != actionPublish && f.Action != actionUnpublish {
		f.Action = actionSave
	}
	return f
}

func postFormOf(p db.Post, coverKey *string) postForm {
	f := postForm{
		Title:     p.Title,
		Slug:      p.Slug,
		Summary:   p.Summary,
		Body:      p.Body,
		Tags:      strings.Join(p.Tags, ", "),
		UpdatedAt: p.UpdatedAt.Format(time.RFC3339Nano),
		Errors:    map[string]string{},
	}
	if coverKey != nil {
		f.Cover = media.URL(*coverKey)
	}
	if p.PublishedAt != nil {
		f.PublishAt = p.PublishedAt.UTC().Format(publishAtLayout)
	}
	return f
}

// validate checks the form on its own. The cover image and whether the
// slug is free are checked against the database when the post is saved.
func (f *postForm) validate() bool {
	switch {
	case f.Title == "":
		f.Errors["title"] = "Enter a title."
	case utf8.RuneCountInString(f.Title) > maxTitleLen:
		f.Errors["title"] = "Use at most 200 characters."
	}
	if f.Slug == "" {
		f.Slug = slug.MakeMax(f.Title, maxSlugLen)
		if f.Slug == "" && f.Title != "" {
			f.Errors["slug"] = "Enter a slug: the title has no letters or digits to make one from."
		}
	} else if !slug.ValidMax(f.Slug, maxSlugLen) {
		f.Errors["slug"] = "Use lowercase letters, digits and single hyphens, at most 80 characters, such as “hello-world”."
	}
	if utf8.RuneCountInString(f.Summary) > maxSummaryLen {
		f.Errors["summary"] = "Use at most 500 characters."
	}
	if len(f.Body) > maxBodyLen {
		f.Errors["body"] = "Use at most 200,000 characters."
	}
	if f.Cover != "" {
		key := strings.TrimPrefix(f.Cover, "/media/")
		if !media.ValidKey(key) {
			f.Errors["cover"] = "Enter an image’s Markdown address from Media, such as /media/ab12cd34."
		}
		f.coverKey = key
	}
	f.tags = []string{}
	for t := range strings.SplitSeq(f.Tags, ",") {
		if t = slug.MakeMax(t, slug.MaxLen); t != "" && !slices.Contains(f.tags, t) {
			f.tags = append(f.tags, t)
		}
	}
	if len(f.tags) > maxTags {
		f.Errors["tags"] = "Use at most 10 tags."
	}
	f.Tags = strings.Join(f.tags, ", ")
	if f.PublishAt != "" {
		t, err := time.Parse(publishAtLayout, f.PublishAt)
		if err != nil {
			f.Errors["published_at"] = "Enter a date and time such as 2026-12-31T09:00."
		} else {
			f.publishAt = &t
		}
	}
	// A missing or mangled time can't match the stored one, so the save is
	// refused as a conflict.
	f.updatedAt, _ = time.Parse(time.RFC3339Nano, f.UpdatedAt)
	return len(f.Errors) == 0
}
