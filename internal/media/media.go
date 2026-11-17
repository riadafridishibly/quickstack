// Package media stores uploaded images and serves resized copies. An upload
// is checked, stored as it came, and turned into WebP variants and a social
// card by a background job that runs libvips.
package media

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/riadafridishibly/quickstack/internal/blob"
	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/jobs"
)

// Asset statuses.
const (
	Processing = "processing"
	Ready      = "ready"
	Failed     = "failed"
)

// Widths are the widths of the WebP variants, as long as the original is
// at least as wide.
var Widths = []int{320, 640, 1024, 1600}

// The social card, og.jpg, is cropped to this size.
const (
	CardWidth  = 1200
	CardHeight = 630
	CardFile   = "og.jpg"
)

// The formats an upload may have, by the libvips loader that reads them.
var formats = map[string]struct{ contentType, ext string }{
	"jpeg": {"image/jpeg", "jpg"},
	"png":  {"image/png", "png"},
	"webp": {"image/webp", "webp"},
	"gif":  {"image/gif", "gif"},
	"heif": {"image/heic", "heic"},
}

func extOf(contentType string) string {
	for _, f := range formats {
		if f.contentType == contentType {
			return f.ext
		}
	}
	return "bin"
}

// crockford is lowercase Crockford base32: no i, l, o or u.
const crockford = "0123456789abcdefghjkmnpqrstvwxyz"

var keyRE = regexp.MustCompile(`^[0-9a-hjkmnp-tv-z]{8}$`)

// ValidKey reports whether s has the form of an asset key.
func ValidKey(s string) bool { return keyRE.MatchString(s) }

func newKey() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = crockford[b[i]&31]
	}
	return string(b)
}

// URL is the path Markdown uses for an asset. The renderer turns it into
// an <img> with the variants.
func URL(key string) string { return "/media/" + key }

// FileURL is the path of one of an asset's files, such as "640.webp".
func FileURL(key, file string) string { return "/media/" + key + "/" + file }

// VariantURL is the path of the WebP variant w wide.
func VariantURL(key string, w int32) string {
	return FileURL(key, strconv.Itoa(int(w))+".webp")
}

// Srcset lists a ready asset's variants for an <img srcset>.
func Srcset(a db.Asset) string {
	parts := make([]string, len(a.Widths))
	for i, w := range a.Widths {
		parts[i] = VariantURL(a.Key, w) + " " + strconv.Itoa(int(w)) + "w"
	}
	return strings.Join(parts, ", ")
}

func originalKey(a db.Asset) string { return a.Key + "/original." + extOf(a.ContentType) }

// Create records up as a new asset, stores the original, and queues the job
// that makes its variants, all as part of tx. If tx doesn't commit, pass
// the returned asset to Discard; its Key is set as soon as a file may have
// been written.
func Create(ctx context.Context, tx pgx.Tx, store blob.Store, jc *jobs.Client, userID int64, up Upload) (db.Asset, error) {
	q := db.New(tx)
	var a db.Asset
	var err error
	for range 5 {
		a, err = q.CreateAsset(ctx, db.CreateAssetParams{
			Key: newKey(), ContentType: up.ContentType, Bytes: int64(len(up.Data)), Sha256: up.SHA256[:],
			Width: int32(up.Width), Height: int32(up.Height), CreatedBy: &userID,
		})
		if !errors.Is(err, pgx.ErrNoRows) {
			break
		}
	}
	if err != nil {
		return db.Asset{}, fmt.Errorf("media: create: %w", err)
	}
	if err := store.Put(ctx, originalKey(a), bytes.NewReader(up.Data), int64(len(up.Data)), up.ContentType); err != nil {
		return a, err
	}
	if _, err := jc.InsertTx(ctx, tx, ProcessArgs{AssetID: a.ID}, nil); err != nil {
		return a, err
	}
	return a, nil
}

// Discard removes the files of an asset whose transaction didn't commit.
func Discard(ctx context.Context, store blob.Store, a db.Asset) {
	if a.Key == "" {
		return
	}
	if err := store.DeletePrefix(context.WithoutCancel(ctx), a.Key+"/"); err != nil {
		slog.ErrorContext(ctx, "media: discard", "key", a.Key, "err", err)
	}
}

// Delete removes the asset with key, as part of tx, and queues a job that
// deletes its files once tx commits. It returns pgx.ErrNoRows if there is
// no such asset.
func Delete(ctx context.Context, tx pgx.Tx, jc *jobs.Client, key string) error {
	if _, err := db.New(tx).DeleteAsset(ctx, key); err != nil {
		return err
	}
	_, err := jc.InsertTx(ctx, tx, DeleteFilesArgs{Key: key}, nil)
	return err
}

// ProcessArgs makes an asset's variants.
type ProcessArgs struct {
	AssetID int64 `json:"asset_id"`
}

func (ProcessArgs) Kind() string { return "media_process" }

func (ProcessArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: jobs.QueueMedia, MaxAttempts: 5}
}

// DeleteFilesArgs deletes the files of a deleted asset.
type DeleteFilesArgs struct {
	Key string `json:"key"`
}

func (DeleteFilesArgs) Kind() string { return "media_delete_files" }

func (DeleteFilesArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: jobs.QueueMedia, MaxAttempts: 10}
}
