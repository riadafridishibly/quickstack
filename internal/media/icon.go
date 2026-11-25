package media

import (
	"bytes"
	"context"
	"encoding/binary"
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
	"github.com/riadafridishibly/quickstack/internal/jobs"
)

// The site icon's files, drawn by the media_icon job into IconDir(rev).
const (
	FaviconFile   = "favicon.ico"
	AppleIconFile = "apple-touch-icon.png"
	// IconSVGFile is there only when the icon is an SVG.
	IconSVGFile = "icon.svg"
)

// AppIconSizes are the sizes of the PNGs the web app manifest lists, named
// by AppIconFile.
var AppIconSizes = []int{192, 512}

func AppIconFile(size int) string { return "icon-" + strconv.Itoa(size) + ".png" }

const (
	faviconSize   = 32
	appleIconSize = 180
)

// IconMinSize is the shortest side a raster icon may have, so that the
// largest PNG isn't scaled up.
const IconMinSize = 512

// IconDir is the blob store prefix of the icon files drawn for rev.
func IconDir(rev int32) string { return "site/icon/" + strconv.Itoa(int(rev)) + "/" }

// IconProblem says why a can't be the site's icon, or returns "" if it can.
func IconProblem(a db.Asset) string {
	switch {
	case a.Status == Processing:
		return "This image is still being processed. Try again in a moment."
	case a.Status != Ready:
		return "This image couldn’t be processed, so it can’t be the icon."
	case IsSVG(a) && !SVGRaster:
		return "This build can’t make the PNG icons that phones and older browsers need from an SVG. Pick a PNG or JPEG at least " +
			strconv.Itoa(IconMinSize) + " pixels on each side, or build with the resvg tag."
	case !IsSVG(a) && min(a.Width, a.Height) < IconMinSize:
		return fmt.Sprintf("This image is %d×%d pixels. An icon needs at least %d on its shorter side.", a.Width, a.Height, IconMinSize)
	}
	return ""
}

// IconArgs draws the site icon's files for rev.
type IconArgs struct {
	Rev int32 `json:"rev"`
}

func (IconArgs) Kind() string { return "media_icon" }

func (IconArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: jobs.QueueMedia, MaxAttempts: 5}
}

type iconWorker struct {
	river.WorkerDefaults[IconArgs]
	pool  *pgxpool.Pool
	store blob.Store
}

func (w *iconWorker) Work(ctx context.Context, j *river.Job[IconArgs]) error {
	return DrawIcon(ctx, w.pool, w.store, j.Args.Rev)
}

// errIconSource is an icon that can't be drawn, however often it's tried.
var errIconSource = errors.New("media: icon source can't be drawn")

// DrawIcon draws the site icon's files for rev and makes them the live
// icon, then deletes the files of the icon they replace. It does nothing
// if rev is no longer the latest change. An error means it should be
// tried again.
func DrawIcon(ctx context.Context, pool *pgxpool.Pool, store blob.Store, rev int32) error {
	q := db.New(pool)
	s, err := q.GetSiteSettings(ctx)
	if err != nil {
		return err
	}
	set := s.SiteSetting
	if set.IconRev != rev || set.IconID == nil || set.LiveIconRev >= rev {
		return nil
	}
	a, err := q.GetAsset(ctx, *set.IconID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if a.Status != Ready {
		return river.JobCancel(fmt.Errorf("%w: asset %s is %s", errIconSource, a.Key, a.Status))
	}
	name := originalKey(a)
	if IsSVG(a) {
		name = a.Key + "/" + SVGFile
	}
	f, err := store.Open(ctx, name)
	if err != nil {
		return err
	}
	data, err := io.ReadAll(f)
	_ = f.Close()
	if err != nil {
		return err
	}
	bg, err := parseHexColor(set.IconBackground)
	if err != nil {
		return river.JobCancel(err)
	}
	files, err := renderIcon(data, IsSVG(a), bg)
	if err != nil {
		slog.WarnContext(ctx, "media: icon not drawn", "key", a.Key, "err", err)
		return river.JobCancel(fmt.Errorf("%w: %w", errIconSource, err))
	}

	dir := IconDir(rev)
	for _, v := range files {
		if err := store.Put(ctx, dir+v.name, bytes.NewReader(v.data), int64(len(v.data)), v.contentType); err != nil {
			return err
		}
	}
	prev, err := q.MarkSiteIconLive(ctx, db.MarkSiteIconLiveParams{IconID: a.ID, Rev: rev})
	if errors.Is(err, pgx.ErrNoRows) {
		// Another icon was chosen, or a later rev went live, while this ran.
		return store.DeletePrefix(ctx, dir)
	}
	if err != nil {
		return err
	}
	if prev > 0 {
		return store.DeletePrefix(ctx, IconDir(prev))
	}
	return nil
}

// iconSource draws an icon as a square of a given size, with the image
// scaled to fit and centred on transparent pixels.
type iconSource interface {
	square(size int, save func(*vips.Image) (file, error)) (file, error)
	close()
}

// renderIcon draws the icon files from a raster image, or from a sanitized
// SVG. bg fills the Apple icon's transparent pixels, which iOS would make
// black.
func renderIcon(data []byte, svg bool, bg []float64) ([]file, error) {
	startVips()
	var src iconSource = rasterIcon(data)
	if svg {
		s, err := newSVGIcon(data)
		if err != nil {
			return nil, err
		}
		src = s
	}
	defer src.close()

	png := func(name string, flatten bool) func(*vips.Image) (file, error) {
		return func(img *vips.Image) (file, error) {
			if flatten {
				if err := img.Flatten(&vips.FlattenOptions{Background: bg, MaxAlpha: 255}); err != nil {
					return file{}, fmt.Errorf("%s: %w", name, err)
				}
			}
			b, err := img.PngsaveBuffer(&vips.PngsaveBufferOptions{Compression: 9, Bitdepth: 8, Keep: vips.KeepNone})
			if err != nil {
				return file{}, fmt.Errorf("%s: %w", name, err)
			}
			return file{name: name, contentType: "image/png", data: b}, nil
		}
	}

	var files []file
	f, err := src.square(faviconSize, png(FaviconFile, false))
	if err != nil {
		return nil, err
	}
	files = append(files, file{name: FaviconFile, contentType: "image/x-icon", data: ico(f.data, faviconSize)})
	if f, err = src.square(appleIconSize, png(AppleIconFile, true)); err != nil {
		return nil, err
	}
	files = append(files, f)
	for _, size := range AppIconSizes {
		if f, err = src.square(size, png(AppIconFile(size), false)); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	if svg {
		files = append(files, file{name: IconSVGFile, contentType: svgContentType, data: data})
	}
	return files, nil
}

type rasterIcon []byte

func (r rasterIcon) close() {}

func (r rasterIcon) square(size int, save func(*vips.Image) (file, error)) (file, error) {
	return thumbnail(r, size, size, vips.InterestingNone, func(img *vips.Image) (file, error) {
		if img.Interpretation() != vips.InterpretationSrgb {
			if err := img.Colourspace(vips.InterpretationSrgb, nil); err != nil {
				return file{}, err
			}
		}
		if !img.HasAlpha() {
			if err := img.Addalpha(); err != nil {
				return file{}, err
			}
		}
		if err := img.Gravity(vips.CompassDirectionCentre, size, size, &vips.GravityOptions{Extend: vips.ExtendBackground, Background: []float64{0, 0, 0, 0}}); err != nil {
			return file{}, err
		}
		return save(img)
	})
}

// ico wraps one PNG in an ICO file: a 6-byte header, then a 16-byte
// directory entry that points at the PNG after it.
func ico(png []byte, size int) []byte {
	const headerSize = 6 + 16
	b := make([]byte, headerSize, headerSize+len(png))
	binary.LittleEndian.PutUint16(b[2:], 1)   // type: icon
	binary.LittleEndian.PutUint16(b[4:], 1)   // images
	b[6], b[7] = byte(size), byte(size)       // 0 would mean 256
	binary.LittleEndian.PutUint16(b[10:], 1)  // colour planes
	binary.LittleEndian.PutUint16(b[12:], 32) // bits per pixel
	binary.LittleEndian.PutUint32(b[14:], uint32(len(png)))
	binary.LittleEndian.PutUint32(b[18:], headerSize)
	return append(b, png...)
}

// parseHexColor reads a colour written #rrggbb.
func parseHexColor(s string) ([]float64, error) {
	if len(s) != 7 || s[0] != '#' {
		return nil, fmt.Errorf("media: colour %q is not #rrggbb", s)
	}
	n, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		return nil, fmt.Errorf("media: colour %q is not #rrggbb", s)
	}
	return []float64{float64(n >> 16), float64(n >> 8 & 0xff), float64(n & 0xff)}, nil
}
