//go:build exclude_gcs

package gcs

import (
	"context"
	"testing"
)

func TestGCSExcluded(t *testing.T) {
	if err := Init(); err != errExcluded {
		t.Errorf("Init() = %v, want %v", err, errExcluded)
	}
	if _, err := NewGoogleStorageClient(context.Background()); err != errExcluded {
		t.Errorf("NewGoogleStorageClient() = %v, want %v", err, errExcluded)
	}
}
