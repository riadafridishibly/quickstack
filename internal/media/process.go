package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"

	vips "github.com/cshum/vipsgen/vips816"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/riadafridishibly/quickstack/internal/blob"
	"github.com/riadafridishibly/quickstack/internal/db"
)

const errUnreadable = "This image couldn’t be read. It may be damaged or cut off; try exporting it again."

// AddWorkers adds the jobs that process and delete assets.
func AddWorkers(w *river.Workers, pool *pgxpool.Pool, store blob.Store) {
	river.AddWorker(w, &processWorker{pool: pool, store: store})
	river.AddWorker(w, &deleteWorker{store: store})
}

type processWorker struct {
	river.WorkerDefaults[ProcessArgs]
	pool  *pgxpool.Pool
	store blob.Store
}

func (w *processWorker) Work(ctx context.Context, j *river.Job[ProcessArgs]) error {
	err := Process(ctx, w.pool, w.store, j.Args.AssetID)
	if err != nil && j.Attempt >= j.MaxAttempts {
		_, ferr := db.New(w.pool).MarkAssetFailed(ctx, db.MarkAssetFailedParams{ID: j.Args.AssetID, Error: "Processing failed. Delete the image and upload it again."})
		return errors.Join(err, ferr)
	}
	return err
}

type deleteWorker struct {
	river.WorkerDefaults[DeleteFilesArgs]
	store blob.Store
}

func (w *deleteWorker) Work(ctx context.Context, j *river.Job[DeleteFilesArgs]) error {
	return w.store.DeletePrefix(ctx, j.Args.Key+"/")
}

// Process makes the variants of the asset with id and marks it ready, or
// failed if the image can't be decoded. An error means it should be tried
// again.
func Process(ctx context.Context, pool *pgxpool.Pool, store blob.Store, id int64) error {
	q := db.New(pool)
	a, err := q.GetAsset(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil || a.Status != Processing {
		return err
	}
	f, err := store.Open(ctx, originalKey(a))
	if err != nil {
		return err
	}
	data, err := io.ReadAll(f)
	_ = f.Close()
	if err != nil {
		return err
	}

	files, width, height, err := render(data)
	if err != nil {
		slog.WarnContext(ctx, "media: unreadable image", "key", a.Key, "err", err)
		_, err := q.MarkAssetFailed(ctx, db.MarkAssetFailedParams{ID: id, Error: errUnreadable})
		return err
	}
	widths := make([]int32, 0, len(files))
	for _, v := range files {
		if err := store.Put(ctx, a.Key+"/"+v.name, bytes.NewReader(v.data), int64(len(v.data)), v.contentType); err != nil {
			return err
		}
		if v.width > 0 {
			widths = append(widths, int32(v.width))
		}
	}
	n, err := q.MarkAssetReady(ctx, db.MarkAssetReadyParams{ID: id, Width: int32(width), Height: int32(height), Widths: widths})
	if err != nil {
		return err
	}
	if n == 0 {
		// Deleted while this ran, after its delete job had already run.
		return store.DeletePrefix(ctx, a.Key+"/")
	}
	return nil
}

type file struct {
	name, contentType string
	data              []byte
	// width is set for the WebP variants.
	width int
}

// variantWidths are the Widths narrower than an image w wide, plus w itself
// if the largest of Widths is wider.
func variantWidths(w int) []int {
	var out []int
	for _, v := range Widths {
		if v < w {
			out = append(out, v)
		}
	}
	if w <= Widths[len(Widths)-1] {
		out = append(out, w)
	}
	return out
}

// render decodes data and encodes the variants and the social card,
// upright, in sRGB, and without metadata. width and height are the size
// of the upright original.
func render(data []byte) (files []file, width, height int, err error) {
	startVips()
	img, err := vips.NewImageFromBuffer(data, &vips.LoadOptions{FailOn: vips.FailOnError})
	if err != nil {
		return nil, 0, 0, err
	}
	width, height = uprightWidth(img), uprightHeight(img)
	img.Close()

	for _, w := range variantWidths(width) {
		b, err := thumbnail(data, w, height, vips.InterestingNone, func(img *vips.Image) ([]byte, error) {
			return img.WebpsaveBuffer(&vips.WebpsaveBufferOptions{Q: 80, Effort: 4, SmartSubsample: true, Keep: vips.KeepNone})
		})
		if err != nil {
			return nil, 0, 0, fmt.Errorf("%d.webp: %w", w, err)
		}
		files = append(files, file{name: strconv.Itoa(w) + ".webp", contentType: "image/webp", data: b, width: w})
	}

	b, err := thumbnail(data, CardWidth, CardHeight, vips.InterestingAttention, func(img *vips.Image) ([]byte, error) {
		if img.HasAlpha() {
			if err := img.Flatten(&vips.FlattenOptions{Background: []float64{255, 255, 255}, MaxAlpha: 255}); err != nil {
				return nil, err
			}
		}
		return img.JpegsaveBuffer(&vips.JpegsaveBufferOptions{Q: 82, OptimizeCoding: true, Interlace: true, Keep: vips.KeepNone})
	})
	if err != nil {
		return nil, 0, 0, fmt.Errorf("%s: %w", CardFile, err)
	}
	files = append(files, file{name: CardFile, contentType: "image/jpeg", data: b})
	return files, width, height, nil
}

// thumbnail scales data to fit w×h, or to fill it and crop when crop isn't
// InterestingNone, and encodes the result with save. It decodes data each
// time, so JPEG and WebP can shrink while loading.
func thumbnail(data []byte, w, h int, crop vips.Interesting, save func(*vips.Image) ([]byte, error)) ([]byte, error) {
	img, err := vips.NewThumbnailBuffer(data, w, &vips.ThumbnailBufferOptions{
		Height:        h,
		Crop:          crop,
		ExportProfile: "srgb",
		Intent:        vips.IntentRelative,
		// thumbnail's own FailOn doesn't reach the loader; this does, so a
		// cut-off file fails instead of coming out half grey.
		OptionString: "fail_on=error",
	})
	if err != nil {
		return nil, err
	}
	defer img.Close()
	return save(img)
}
