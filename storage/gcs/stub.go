//go:build exclude_gcs

package gcs

import (
	"context"
	"errors"

	"github.com/tingdahl/goutils/storage"
)

var errExcluded = errors.New("gcs storage provider is excluded at compile time via exclude_gcs build tag")

// Init returns an error because GCS support was excluded at build time.
func Init() error {
	return errExcluded
}

// NewGoogleStorageClient returns an error because GCS support was excluded at build time.
func NewGoogleStorageClient(ctx context.Context) (storage.StorageClient, error) {
	return nil, errExcluded
}
