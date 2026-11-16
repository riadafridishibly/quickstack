// Package blob stores files by key, on local disk or in an S3-compatible
// bucket.
package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
)

// ErrNotFound is returned by Open when no file has the key.
var ErrNotFound = errors.New("blob: not found")

// Store holds files by key. A key is a slash-separated relative path such as
// "abcd1234/640.webp"; CheckKey says which keys are allowed.
type Store interface {
	// Put stores size bytes from r under key, replacing any file there.
	// Readers never see a partly written file.
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	// Open returns the file at key, or ErrNotFound.
	Open(ctx context.Context, key string) (io.ReadSeekCloser, error)
	// DeletePrefix deletes every file whose key starts with prefix, which
	// is a directory ending in "/", such as "abcd1234/".
	DeletePrefix(ctx context.Context, prefix string) error
}

// Open returns the store rawURL names:
//
//	file:///var/lib/quickstack/media   an absolute directory
//	file://./tmp/media                 a directory relative to the working directory
//	s3://[key:secret@]bucket[/prefix][?endpoint=host:port&region=r&insecure=1]
func Open(rawURL string) (Store, error) {
	if dir, ok := strings.CutPrefix(rawURL, "file://"); ok {
		return OpenFile(dir)
	}
	if strings.HasPrefix(rawURL, "s3://") {
		return openS3(rawURL)
	}
	return nil, fmt.Errorf("blob: %q is not a file:// or s3:// URL", rawURL)
}

// CheckKey rejects keys that could name something outside the store's
// root: absolute paths, "." and ".." elements, empty elements, and
// backslashes.
func CheckKey(key string) error {
	if !fs.ValidPath(key) || key == "." || strings.Contains(key, `\`) {
		return fmt.Errorf("blob: invalid key %q", key)
	}
	return nil
}

func checkPrefix(prefix string) error {
	dir, ok := strings.CutSuffix(prefix, "/")
	if !ok {
		return fmt.Errorf("blob: prefix %q must end in /", prefix)
	}
	return CheckKey(dir)
}
