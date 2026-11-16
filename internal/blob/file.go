package blob

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
)

// File keeps files in a directory on local disk.
type File struct {
	root *os.Root
}

// OpenFile opens dir as a store, creating it if needed. Every operation
// goes through an os.Root, so no key can reach outside dir.
func OpenFile(dir string) (*File, error) {
	if dir == "" {
		return nil, errors.New("blob: empty directory")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("blob: %w", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("blob: %w", err)
	}
	return &File{root: root}, nil
}

// Put writes to a temporary file next to key, syncs it, and renames it into
// place.
func (f *File) Put(_ context.Context, key string, r io.Reader, size int64, _ string) error {
	if err := CheckKey(key); err != nil {
		return err
	}
	dir := path.Dir(key)
	if err := f.root.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("blob: %w", err)
	}
	tmp := path.Join(dir, ".tmp-"+rand.Text())
	w, err := f.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return fmt.Errorf("blob: %w", err)
	}
	err = write(w, r, size)
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = f.root.Rename(tmp, key)
	}
	if err != nil {
		_ = f.root.Remove(tmp)
		return fmt.Errorf("blob: put %s: %w", key, err)
	}
	return nil
}

func write(w *os.File, r io.Reader, size int64) error {
	n, err := io.Copy(w, r)
	if err != nil {
		return err
	}
	if n != size {
		return fmt.Errorf("wrote %d bytes, want %d", n, size)
	}
	return w.Sync()
}

func (f *File) Open(_ context.Context, key string) (io.ReadSeekCloser, error) {
	if err := CheckKey(key); err != nil {
		return nil, err
	}
	file, err := f.root.Open(key)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("blob: %w", err)
	}
	if st, err := file.Stat(); err != nil || st.IsDir() {
		_ = file.Close()
		return nil, ErrNotFound
	}
	return file, nil
}

func (f *File) DeletePrefix(_ context.Context, prefix string) error {
	if err := checkPrefix(prefix); err != nil {
		return err
	}
	if err := f.root.RemoveAll(path.Clean(prefix)); err != nil {
		return fmt.Errorf("blob: %w", err)
	}
	return nil
}
