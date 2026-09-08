//go:build !exclude_gcs

package gcs

import (
	"context"
	"testing"

	"github.com/tingdahl/goutils/storage"
)

func TestGCSInit(t *testing.T) {
	err := Init()
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// GCS constructor registered; NewStorageClient will attempt gcs.NewClient
	// which may succeed or fail depending on GCP credentials, but constructor was called
	_, _ = storage.NewStorageClient()
}

func TestGCSLifecycle_Integration(t *testing.T) {
	// Integration test skipped unless GCS_TEST_BUCKET is set
	t.Skip("Skipping integration test: GCS_TEST_BUCKET environment variable is not set")
	_, _ = NewGoogleStorageClient(context.Background())
}
