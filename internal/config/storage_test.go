// Tests the storage configuration: backend choice, S3 settings and their validation.
package config

import (
	"os"
	"strings"
	"testing"
)

func setStorageEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for _, key := range []string{storageBackendKey, storageDirKey, s3BucketKey, s3EndpointKey, s3RegionKey, s3PathStyleKey, s3PrefixKey} {
		t.Setenv(key, env[key])
	}
}

func TestStorageDefaultsToTheTempDirectory(t *testing.T) {
	setStorageEnv(t, nil)

	got, err := loadStorage()

	if err != nil || got.Backend != BackendFS || got.Dir != os.TempDir() {
		t.Errorf("got (%+v, %v)", got, err)
	}
}

func TestStorageFilesystemDirectory(t *testing.T) {
	setStorageEnv(t, map[string]string{storageBackendKey: "fs", storageDirKey: "/data/cache"})

	got, err := loadStorage()

	if err != nil || got.Dir != "/data/cache" {
		t.Errorf("got (%+v, %v)", got, err)
	}
}

func TestStorageS3(t *testing.T) {
	setStorageEnv(t, map[string]string{
		storageBackendKey: "s3",
		s3BucketKey:       "nx-cache",
		s3EndpointKey:     "http://localhost:3900",
		s3RegionKey:       "garage",
		s3PathStyleKey:    "true",
		s3PrefixKey:       "/prod/",
	})

	got, err := loadStorage()

	want := S3Storage{Bucket: "nx-cache", Endpoint: "http://localhost:3900", Region: "garage", PathStyle: true, Prefix: "prod"}
	if err != nil || got.Backend != BackendS3 || got.S3 != want {
		t.Errorf("got (%+v, %v), want %+v", got, err, want)
	}
}

func TestStorageS3Defaults(t *testing.T) {
	setStorageEnv(t, map[string]string{storageBackendKey: "s3", s3BucketKey: "b"})

	got, err := loadStorage()

	if err != nil || got.S3.Region != "us-east-1" || got.S3.PathStyle || got.S3.Endpoint != "" {
		t.Errorf("got (%+v, %v)", got, err)
	}
}

func TestStorageErrors(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"unknown backend", map[string]string{storageBackendKey: "gcs"}, storageBackendKey},
		{"missing bucket", map[string]string{storageBackendKey: "s3"}, s3BucketKey},
		{"endpoint without scheme", map[string]string{storageBackendKey: "s3", s3BucketKey: "b", s3EndpointKey: "localhost:3900"}, s3EndpointKey},
		{"endpoint with another scheme", map[string]string{storageBackendKey: "s3", s3BucketKey: "b", s3EndpointKey: "ftp://x"}, s3EndpointKey},
		{"prefix with a space", map[string]string{storageBackendKey: "s3", s3BucketKey: "b", s3PrefixKey: "my cache"}, s3PrefixKey},
		{"prefix with a relative segment", map[string]string{storageBackendKey: "s3", s3BucketKey: "b", s3PrefixKey: "a//b"}, s3PrefixKey},
		{"bad path style", map[string]string{storageBackendKey: "s3", s3BucketKey: "b", s3PathStyleKey: "maybe"}, s3PathStyleKey},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setStorageEnv(t, tt.env)

			if _, err := loadStorage(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want one naming %s", err, tt.want)
			}
		})
	}
}

func TestStorageReportsEveryS3Problem(t *testing.T) {
	setStorageEnv(t, map[string]string{storageBackendKey: "s3", s3EndpointKey: "nope", s3PathStyleKey: "x"})

	_, err := loadStorage()

	for _, key := range []string{s3BucketKey, s3EndpointKey, s3PathStyleKey} {
		if err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("error %v does not name %s", err, key)
		}
	}
}
