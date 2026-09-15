//go:build exclude_gcs

package gcs

import (
	"testing"
)

func TestGCSExcluded(t *testing.T) {
	if err := Init(); err != errExcluded {
		t.Errorf("Init() = %v, want %v", err, errExcluded)
	}
	if _, err := NewGoogleStorageClient(nil); err != errExcluded {
		t.Errorf("NewGoogleStorageClient() = %v, want %v", err, errExcluded)
	}
}
