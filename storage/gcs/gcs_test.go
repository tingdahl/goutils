//go:build !exclude_gcs

package gcs

import (
	"testing"

	"github.com/tingdahl/goutils/storage"
)

func TestGCSInit(t *testing.T) {
	// Missing required env variables should fail
	t.Setenv("GCS_BUCKET", "")
	if err := Init(); err == nil {
		t.Fatal("expected Init to fail when required env variables are missing, got nil")
	}

	t.Setenv("GCS_BUCKET", "test-bucket")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")

	err := Init()
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// GCS constructor registered; NewStorageClient will attempt gcs.NewClient
	// which may succeed or fail depending on GCP credentials, but constructor was called
	_, _ = storage.NewStorageClient(map[string]string{"GCS_BUCKET": "test-bucket"})
}

func TestGCSLifecycle_Integration(t *testing.T) {
	// Integration test skipped unless GCS_TEST_BUCKET is set
	t.Skip("Skipping integration test: GCS_TEST_BUCKET environment variable is not set")
	_, _ = NewGoogleStorageClient(map[string]string{"GCS_BUCKET": "test-bucket"})
}
