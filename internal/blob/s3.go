package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3 keeps files in a bucket of any S3-compatible service: AWS S3,
// Cloudflare R2, Backblaze B2, SeaweedFS, Garage and others.
type S3 struct {
	client *minio.Client
	bucket string
	prefix string
}

func openS3(rawURL string) (*S3, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return nil, errors.New("blob: an S3 URL is s3://[key:secret@]bucket[/prefix][?endpoint=host:port&region=r&insecure=1]")
	}
	q := u.Query()
	endpoint := q.Get("endpoint")
	if endpoint == "" {
		endpoint = "s3.amazonaws.com"
	}
	var creds *credentials.Credentials
	if u.User != nil {
		secret, _ := u.User.Password()
		creds = credentials.NewStaticV4(u.User.Username(), secret, "")
	} else {
		creds = credentials.NewChainCredentials([]credentials.Provider{
			&credentials.EnvAWS{},
			&credentials.FileAWSCredentials{},
			&credentials.IAM{},
		})
	}
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  creds,
		Secure: q.Get("insecure") != "1",
		Region: q.Get("region"),
	})
	if err != nil {
		return nil, fmt.Errorf("blob: %w", err)
	}
	prefix := strings.Trim(u.Path, "/")
	if prefix != "" {
		if err := CheckKey(prefix); err != nil {
			return nil, err
		}
		prefix += "/"
	}
	return &S3{client: client, bucket: u.Host, prefix: prefix}, nil
}

func (s *S3) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	if err := CheckKey(key); err != nil {
		return err
	}
	_, err := s.client.PutObject(ctx, s.bucket, s.prefix+key, r, size, minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return fmt.Errorf("blob: put %s: %w", key, err)
	}
	return nil
}

func (s *S3) Open(ctx context.Context, key string) (io.ReadSeekCloser, error) {
	if err := CheckKey(key); err != nil {
		return nil, err
	}
	obj, err := s.client.GetObject(ctx, s.bucket, s.prefix+key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("blob: open %s: %w", key, err)
	}
	// GetObject sends no request until the first read; Stat finds out
	// whether the object exists.
	if _, err := obj.Stat(); err != nil {
		_ = obj.Close()
		if minio.ToErrorResponse(err).Code == minio.NoSuchKey {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("blob: open %s: %w", key, err)
	}
	return obj, nil
}

func (s *S3) DeletePrefix(ctx context.Context, prefix string) error {
	if err := checkPrefix(prefix); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	objects := s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: s.prefix + prefix, Recursive: true})
	toDelete := make(chan minio.ObjectInfo)
	listErr := make(chan error, 1)
	go func() {
		defer close(toDelete)
		for o := range objects {
			if o.Err != nil {
				listErr <- o.Err
				return
			}
			select {
			case toDelete <- o:
			case <-ctx.Done():
				listErr <- ctx.Err()
				return
			}
		}
		listErr <- nil
	}()
	var errs []error
	for e := range s.client.RemoveObjects(ctx, s.bucket, toDelete, minio.RemoveObjectsOptions{}) {
		errs = append(errs, fmt.Errorf("%s: %w", e.ObjectName, e.Err))
	}
	cancel()
	if err := <-listErr; err != nil && !errors.Is(err, context.Canceled) {
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("blob: delete %s: %w", prefix, err)
	}
	return nil
}
