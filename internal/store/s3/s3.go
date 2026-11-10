// S3 backend: an entry is the object <prefix><key>.cache of one bucket.
// Writes check the key first and are conditional (If-None-Match), so the first writer of a key wins and no partial entry is visible.
// Bodies are streamed to the bucket one part at a time, never held whole.
package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"nx-caching-server/internal/config"
	"nx-caching-server/internal/store"
)

const (
	suffix = ".cache"
	// partSize is the most memory an upload holds: a part is read from the
	// request, then sent, while S3 requires every part but the last to be 5 MiB.
	partSize = 8 << 20
	// uploadConcurrency is how many parts of one upload are in flight at once.
	uploadConcurrency = 3
)

// partBuffers recycles part buffers across uploads.
var partBuffers = sync.Pool{New: func() any {
	buf := make([]byte, partSize)
	return &buf
}}

// Options tunes the store.
type Options struct {
	// TouchAfter is how old an entry must be before a read refreshes its
	// modification time, which costs a request. Zero refreshes on every read.
	TouchAfter time.Duration
}

// Store keeps its entries in one bucket.
type Store struct {
	client     *s3.Client
	bucket     string
	prefix     string
	touchAfter time.Duration
}

var (
	_ store.Store        = (*Store)(nil)
	_ store.Sweeper      = (*Store)(nil)
	_ store.BatchDeleter = (*Store)(nil)
)

// New connects to the bucket described by cfg. Credentials come from the AWS
// SDK's standard sources (AWS_ACCESS_KEY_ID, shared files, instance roles).
func New(ctx context.Context, cfg config.S3Storage, opts Options) (*Store, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("loading the AWS configuration: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = cfg.PathStyle
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
		// Self-hosted services often reject the integrity trailers the SDK adds by default.
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	})
	return &Store{client: client, bucket: cfg.Bucket, prefix: cfg.Prefix, touchAfter: opts.TouchAfter}, nil
}

func (s *Store) objectKey(key string) (string, error) {
	if err := store.ValidKey(key); err != nil {
		return "", err
	}
	return path.Join(s.prefix, key) + suffix, nil
}

func (s *Store) Get(ctx context.Context, key string) (store.Object, error) {
	objectKey, err := s.objectKey(key)
	if err != nil {
		return store.Object{}, err
	}

	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: &objectKey})
	if isNotFound(err) {
		return store.Object{}, store.ErrNotFound
	}
	if err != nil {
		return store.Object{}, err
	}

	if time.Since(aws.ToTime(out.LastModified)) > s.touchAfter {
		// The download does not wait for the refresh, nor does it cancel it.
		go s.touch(context.WithoutCancel(ctx), objectKey)
	}
	return store.Object{ReadCloser: out.Body, Size: aws.ToInt64(out.ContentLength)}, nil
}

// touch refreshes the modification time by copying the object onto itself.
func (s *Store) touch(ctx context.Context, objectKey string) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	_, err := s.client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:            &s.bucket,
		Key:               &objectKey,
		CopySource:        aws.String(s.bucket + "/" + objectKey),
		MetadataDirective: types.MetadataDirectiveReplace,
	})
	if err != nil {
		slog.Warn("failed to update the access time", slog.String("key", objectKey), slog.Any("error", err))
	}
}

func (s *Store) Put(ctx context.Context, key string, body io.Reader, size int64) error {
	objectKey, err := s.objectKey(key)
	if err != nil {
		return err
	}

	// Services that ignore If-None-Match (Garage) only get the existence check,
	// which two simultaneous writers of one key can both pass; the last write then wins.
	if size <= partSize {
		return s.putObject(ctx, objectKey, body, size)
	}
	if err := s.checkAbsent(ctx, objectKey); err != nil {
		return err
	}
	return s.putMultipart(ctx, body, size, objectKey)
}

// checkAbsent returns store.ErrExists when the object is already there.
func (s *Store) checkAbsent(ctx context.Context, objectKey string) error {
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &s.bucket, Key: &objectKey})
	if err == nil {
		return store.ErrExists
	}
	if isNotFound(err) {
		return nil
	}
	return err
}

// putObject sends a body that fits one part in a single request. The existence
// check runs while the body is read, so it costs no extra round trip.
func (s *Store) putObject(ctx context.Context, objectKey string, body io.Reader, size int64) error {
	absent := make(chan error, 1)
	go func() { absent <- s.checkAbsent(ctx, objectKey) }()

	buf := make([]byte, size)
	_, readErr := io.ReadFull(body, buf)
	if err := <-absent; err != nil {
		return err
	}
	if readErr != nil {
		return readErr
	}

	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        &s.bucket,
		Key:           &objectKey,
		Body:          bytes.NewReader(buf),
		ContentLength: aws.Int64(size),
		ContentType:   aws.String("application/octet-stream"),
		IfNoneMatch:   aws.String("*"),
	})
	return conflictAsExists(err)
}

// putMultipart reads body one part at a time and sends up to uploadConcurrency
// parts at once, so reading the request overlaps the transfer to the bucket.
// The entry only appears when the upload is completed, which carries the condition.
func (s *Store) putMultipart(ctx context.Context, body io.Reader, size int64, objectKey string) (err error) {
	created, err := s.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket:      &s.bucket,
		Key:         &objectKey,
		ContentType: aws.String("application/octet-stream"),
	})
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			s.abort(objectKey, created.UploadId)
		}
	}()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		parts    []types.CompletedPart
		firstErr error
		slots    = make(chan struct{}, uploadConcurrency)
	)
	fail := func(err error) {
		mu.Lock()
		defer mu.Unlock()
		if firstErr == nil {
			firstErr = err
			cancel()
		}
	}

	number := int32(0)
	for sent := int64(0); sent < size && ctx.Err() == nil; {
		// A slot is taken before the buffer is filled, which caps the memory in use.
		slots <- struct{}{}
		buf := partBuffers.Get().(*[]byte)
		chunk := (*buf)[:min(int64(partSize), size-sent)]
		if _, err := io.ReadFull(body, chunk); err != nil {
			partBuffers.Put(buf)
			<-slots
			fail(err)
			break
		}
		sent += int64(len(chunk))
		number++

		wg.Add(1)
		go func(number int32) {
			defer wg.Done()
			defer func() { partBuffers.Put(buf); <-slots }()

			uploaded, err := s.client.UploadPart(ctx, &s3.UploadPartInput{
				Bucket:        &s.bucket,
				Key:           &objectKey,
				UploadId:      created.UploadId,
				PartNumber:    aws.Int32(number),
				Body:          bytes.NewReader(chunk),
				ContentLength: aws.Int64(int64(len(chunk))),
			})
			if err != nil {
				fail(err)
				return
			}
			mu.Lock()
			parts = append(parts, types.CompletedPart{ETag: uploaded.ETag, PartNumber: aws.Int32(number)})
			mu.Unlock()
		}(number)
	}
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}

	slices.SortFunc(parts, func(a, b types.CompletedPart) int { return int(aws.ToInt32(a.PartNumber) - aws.ToInt32(b.PartNumber)) })
	_, err = s.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:          &s.bucket,
		Key:             &objectKey,
		UploadId:        created.UploadId,
		MultipartUpload: &types.CompletedMultipartUpload{Parts: parts},
		IfNoneMatch:     aws.String("*"),
	})
	return conflictAsExists(err)
}

// abort discards the parts of a failed upload; the request context may be gone.
func (s *Store) abort(objectKey string, uploadID *string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := s.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{Bucket: &s.bucket, Key: &objectKey, UploadId: uploadID})
	if err != nil {
		slog.Warn("failed to abort an upload", slog.String("key", objectKey), slog.Any("error", err))
	}
}

func conflictAsExists(err error) error {
	if isConflict(err) {
		return store.ErrExists
	}
	return err
}

func (s *Store) Walk(ctx context.Context, fn func(store.Entry) error) error {
	prefix := ""
	if s.prefix != "" {
		prefix = s.prefix + "/"
	}

	pages := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{Bucket: &s.bucket, Prefix: &prefix})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, object := range page.Contents {
			name := aws.ToString(object.Key)
			if !strings.HasSuffix(name, suffix) {
				continue
			}
			key := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
			if err := fn(store.Entry{Key: key, ModTime: aws.ToTime(object.LastModified)}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) Delete(ctx context.Context, key string) error {
	objectKey, err := s.objectKey(key)
	if err != nil {
		return err
	}
	_, err = s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &s.bucket, Key: &objectKey})
	return err
}

// deleteBatch is the most keys S3 accepts in one DeleteObjects request.
const deleteBatch = 1000

func (s *Store) DeleteMany(ctx context.Context, keys []string) error {
	var errs []error
	for start := 0; start < len(keys); start += deleteBatch {
		batch := keys[start:min(start+deleteBatch, len(keys))]

		objects := make([]types.ObjectIdentifier, 0, len(batch))
		for _, key := range batch {
			objectKey, err := s.objectKey(key)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			objects = append(objects, types.ObjectIdentifier{Key: aws.String(objectKey)})
		}
		if len(objects) == 0 {
			continue
		}

		out, err := s.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: &s.bucket,
			Delete: &types.Delete{Objects: objects, Quiet: aws.Bool(true)},
		})
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, failed := range out.Errors {
			errs = append(errs, fmt.Errorf("deleting %s: %s", aws.ToString(failed.Key), aws.ToString(failed.Message)))
		}
	}
	return errors.Join(errs...)
}

func isNotFound(err error) bool {
	var noSuchKey *types.NoSuchKey
	if errors.As(err, &noSuchKey) {
		return true
	}
	var apiErr smithy.APIError
	return errors.As(err, &apiErr) && (apiErr.ErrorCode() == "NoSuchKey" || apiErr.ErrorCode() == "NotFound")
}

// isConflict reports a failed If-None-Match: 412, or 409 while another write of
// the same key is in flight.
func isConflict(err error) bool {
	var response *smithyhttp.ResponseError
	if !errors.As(err, &response) {
		return false
	}
	code := response.HTTPStatusCode()
	return code == http.StatusPreconditionFailed || code == http.StatusConflict
}
