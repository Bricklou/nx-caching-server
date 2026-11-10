// Where cache entries are kept: a directory or an S3 compatible bucket, chosen by STORAGE_BACKEND.
// S3 credentials are not read here: the AWS SDK finds them in its standard environment variables.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
)

const (
	storageBackendKey = "STORAGE_BACKEND"
	storageDirKey     = "STORAGE_DIR"
	s3BucketKey       = "S3_BUCKET"
	s3EndpointKey     = "S3_ENDPOINT"
	s3RegionKey       = "S3_REGION"
	s3PathStyleKey    = "S3_PATH_STYLE"
	s3PrefixKey       = "S3_PREFIX"

	// BackendFS keeps entries as files in a directory.
	BackendFS = "fs"
	// BackendS3 keeps entries as objects of an S3 compatible bucket.
	BackendS3 = "s3"

	defaultS3Region = "us-east-1"
)

// s3PrefixPattern keeps the prefix free of characters that need escaping in a copy source.
var s3PrefixPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)

// Storage selects and configures the entry store.
type Storage struct {
	// Backend is BackendFS or BackendS3.
	Backend string
	// Dir is the root directory of the fs backend.
	Dir string
	S3  S3Storage
}

// S3Storage locates a bucket.
type S3Storage struct {
	Bucket string
	// Endpoint is empty for AWS, and the URL of the service otherwise.
	Endpoint string
	Region   string
	// PathStyle addresses the bucket in the URL path instead of the host name,
	// which most self-hosted services need.
	PathStyle bool
	// Prefix is prepended to every object key, to share a bucket.
	Prefix string
}

func loadStorage() (Storage, error) {
	storage := Storage{
		Backend: envOr(storageBackendKey, BackendFS),
		Dir:     envOr(storageDirKey, os.TempDir()),
	}

	switch storage.Backend {
	case BackendFS:
		return storage, nil
	case BackendS3:
		s3, err := loadS3Storage()
		storage.S3 = s3
		return storage, err
	}
	return Storage{}, fmt.Errorf("%s: %q must be %q or %q", storageBackendKey, storage.Backend, BackendFS, BackendS3)
}

func loadS3Storage() (S3Storage, error) {
	s3 := S3Storage{
		Bucket:   os.Getenv(s3BucketKey),
		Endpoint: os.Getenv(s3EndpointKey),
		Region:   envOr(s3RegionKey, defaultS3Region),
		Prefix:   strings.Trim(os.Getenv(s3PrefixKey), "/"),
	}

	var errs []error
	if s3.Bucket == "" {
		errs = append(errs, fmt.Errorf("%s is required with %s=%s", s3BucketKey, storageBackendKey, BackendS3))
	}
	if s3.Endpoint != "" {
		if u, err := url.Parse(s3.Endpoint); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, fmt.Errorf("%s: %q must be an http or https URL", s3EndpointKey, s3.Endpoint))
		}
	}
	if s3.Prefix != "" && !s3PrefixPattern.MatchString(s3.Prefix) {
		errs = append(errs, fmt.Errorf("%s: %q may only hold letters, digits, '.', '_', '-' and '/' between names", s3PrefixKey, s3.Prefix))
	}
	if raw := os.Getenv(s3PathStyleKey); raw != "" {
		pathStyle, err := strconv.ParseBool(raw)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %q must be true or false", s3PathStyleKey, raw))
		}
		s3.PathStyle = pathStyle
	}
	return s3, errors.Join(errs...)
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
