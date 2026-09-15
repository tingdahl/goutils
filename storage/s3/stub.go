//go:build exclude_s3

package s3

import (
	"errors"

	"github.com/tingdahl/goutils/storage"
)

var errExcluded = errors.New("s3 storage provider is excluded at compile time via exclude_s3 build tag")

// Init returns an error because S3 support was excluded at build time.
func Init() error {
	return errExcluded
}

// NewS3StorageClient returns an error because S3 support was excluded at build time.
func NewS3StorageClient(opts map[string]string) (storage.StorageClient, error) {
	return nil, errExcluded
}
