package blob

import (
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minio/minio-go/v7"
)

func TestFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := Open("file://" + dir)
	if err != nil {
		t.Fatal(err)
	}
	testStore(t, s)
	if entries, _ := os.ReadDir(filepath.Join(dir, "a")); len(entries) != 0 {
		t.Errorf("files left in a/: %v", entries)
	}
}

// TestS3 runs against the bucket in QUICKSTACK_TEST_S3_URL, such as
// s3://key:secret@quickstack-test?endpoint=localhost:8333&insecure=1 for
// `make s3-up`.
func TestS3(t *testing.T) {
	t.Parallel()
	raw := os.Getenv("QUICKSTACK_TEST_S3_URL")
	if raw == "" {
		t.Skip("QUICKSTACK_TEST_S3_URL is not set")
	}
	s, err := openS3(raw)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := s.client.BucketExists(t.Context(), s.bucket)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		if err := s.client.MakeBucket(t.Context(), s.bucket, minio.MakeBucketOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	s.prefix += "test-" + strings.ToLower(rand.Text()[:10]) + "/"
	testStore(t, s)
}

func testStore(t *testing.T, s Store) {
	ctx := t.Context()
	put := func(key, body string) {
		t.Helper()
		if err := s.Put(ctx, key, strings.NewReader(body), int64(len(body)), "text/plain"); err != nil {
			t.Fatal(err)
		}
	}
	read := func(key string) (string, error) {
		t.Helper()
		f, err := s.Open(ctx, key)
		if err != nil {
			return "", err
		}
		defer f.Close()
		b, err := io.ReadAll(f)
		return string(b), err
	}

	put("a/1.txt", "one")
	put("a/2.txt", "two")
	put("a/1.txt", "one again")
	put("b/1.txt", "other")
	if got, err := read("a/1.txt"); err != nil || got != "one again" {
		t.Errorf("read a/1.txt = %q, %v", got, err)
	}

	f, err := s.Open(ctx, "a/2.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(1, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(f); string(b) != "wo" {
		t.Errorf("after seek: %q", b)
	}
	_ = f.Close()

	for _, key := range []string{"a/3.txt", "a"} {
		if _, err := read(key); !errors.Is(err, ErrNotFound) {
			t.Errorf("open %s: %v, want ErrNotFound", key, err)
		}
	}
	if err := s.Put(ctx, "a/short.txt", strings.NewReader("ab"), 3, "text/plain"); err == nil {
		t.Error("put with the wrong size succeeded")
	}

	if err := s.DeletePrefix(ctx, "a/"); err != nil {
		t.Fatal(err)
	}
	if _, err := read("a/1.txt"); !errors.Is(err, ErrNotFound) {
		t.Errorf("after delete: %v", err)
	}
	if got, err := read("b/1.txt"); err != nil || got != "other" {
		t.Errorf("b/1.txt after deleting a/: %q, %v", got, err)
	}
	if err := s.DeletePrefix(ctx, "nothing/"); err != nil {
		t.Errorf("delete an empty prefix: %v", err)
	}
	if err := s.DeletePrefix(ctx, "b/"); err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{"", "/etc/passwd", "../x", "a/../../x", "a//b", "./a", `a\..\x`, "a/"} {
		if err := s.Put(ctx, key, strings.NewReader("x"), 1, "text/plain"); err == nil {
			t.Errorf("put %q succeeded", key)
		}
		if _, err := s.Open(ctx, key); err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("open %q: %v, want an invalid key", key, err)
		}
	}
	for _, prefix := range []string{"", "/", "a", "../", "a/../"} {
		if err := s.DeletePrefix(ctx, prefix); err == nil {
			t.Errorf("delete prefix %q succeeded", prefix)
		}
	}
}

func TestOpenURL(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"", "/var/media", "http://x", "s3://", "s3://bucket/../x"} {
		if _, err := Open(bad); err == nil {
			t.Errorf("Open(%q) succeeded", bad)
		}
	}
	s, err := openS3("s3://id:secret@media/site/a?endpoint=localhost:8333&insecure=1&region=eu")
	if err != nil {
		t.Fatal(err)
	}
	if s.bucket != "media" || s.prefix != "site/a/" || s.client.EndpointURL().String() != "http://localhost:8333" {
		t.Errorf("bucket %q, prefix %q, endpoint %s", s.bucket, s.prefix, s.client.EndpointURL())
	}
}
