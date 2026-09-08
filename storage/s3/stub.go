//go:build exclude_s3

package s3

import (
	"context"
	"errors"

	"github.com/tingdahl/goutils/storage"
)

var errExcluded = errors.New("s3 storage provider is excluded at compile time via exclude_s3 build tag")

// Init returns an error because S3 support was excluded at build time.
func Init(region string, endpoint string) error {
	return errExcluded
}

// NewS3StorageClient returns an error because S3 support was excluded at build time.
func NewS3StorageClient(ctx context.Context, region string, endpoint string) (storage.StorageClient, error) {
	return nil, errExcluded
}
