package media

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"

	vips "github.com/cshum/vipsgen/vips816"
)

const (
	// MaxBytes is the largest file an upload may be.
	MaxBytes = 20 << 20
	// MaxPixels is the most pixels an upload may have, which bounds the
	// memory a job needs to decode it.
	MaxPixels = 50_000_000
)

// Upload is an image that passed Check.
type Upload struct {
	Data        []byte
	ContentType string
	SHA256      [32]byte
	// Width and Height are the image's size once turned upright.
	Width, Height int
}

// Rejected is an upload Check refused. Msg is written for the person
// uploading.
type Rejected struct{ Msg string }

func (e *Rejected) Error() string { return e.Msg }

var (
	errNoFile   = &Rejected{"Choose an image to upload."}
	errEmpty    = &Rejected{"This file is empty."}
	errTooLarge = &Rejected{"This file is larger than 20 MB. Make it smaller and try again."}
	errFormat   = &Rejected{"This file isn’t an image we can use. Upload a JPEG, PNG, WebP, GIF or HEIC file."}
)

// Check reads the multipart form field "file" from r and checks that it is
// an image the job can process. It returns a *Rejected error for files it
// refuses. Limit r.Body to a little over MaxBytes before calling it.
func Check(r *http.Request) (Upload, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return Upload{}, errNoFile
	}
	for {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return Upload{}, errNoFile
		}
		if err != nil {
			return Upload{}, readError(err)
		}
		if p.FormName() != "file" {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(p, MaxBytes+1))
		if err != nil {
			return Upload{}, readError(err)
		}
		if p.FileName() == "" {
			return Upload{}, errNoFile
		}
		return CheckData(data)
	}
}

func readError(err error) error {
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return errTooLarge
	}
	return err
}

// CheckData checks an image read some other way, as Check does.
func CheckData(data []byte) (Upload, error) {
	switch {
	case len(data) == 0:
		return Upload{}, errEmpty
	case len(data) > MaxBytes:
		return Upload{}, errTooLarge
	}
	format, w, h, err := identify(data)
	if err != nil {
		return Upload{}, errFormat
	}
	f, ok := formats[format]
	if !ok {
		return Upload{}, errFormat
	}
	if int64(w)*int64(h) > MaxPixels {
		return Upload{}, &Rejected{fmt.Sprintf("This image is %d megapixels, and the limit is 50. Make it smaller and try again.", int64(w)*int64(h)/1_000_000)}
	}
	return Upload{Data: data, ContentType: f.contentType, SHA256: sha256.Sum256(data), Width: w, Height: h}, nil
}

// identify reads the image's header: its format and its size once turned
// upright. The pixels aren't decoded.
func identify(data []byte) (format string, width, height int, err error) {
	startVips()
	img, err := vips.NewImageFromBuffer(data, &vips.LoadOptions{FailOn: vips.FailOnError})
	if err != nil {
		return "", 0, 0, err
	}
	defer img.Close()
	return string(img.Format()), uprightWidth(img), uprightHeight(img), nil
}

// EXIF orientations 5 to 8 turn the image a quarter.
func turned(img *vips.Image) bool { return img.Orientation() >= 5 && img.Orientation() <= 8 }

func uprightWidth(img *vips.Image) int {
	if turned(img) {
		return img.Height()
	}
	return img.Width()
}

func uprightHeight(img *vips.Image) int {
	if turned(img) {
		return img.Width()
	}
	return img.Height()
}
