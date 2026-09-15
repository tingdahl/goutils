//go:build exclude_s3

package s3

import (
	"testing"
)

func TestS3Excluded(t *testing.T) {
	if err := Init(); err != errExcluded {
		t.Errorf("Init() = %v, want %v", err, errExcluded)
	}
	if _, err := NewS3StorageClient(nil); err != errExcluded {
		t.Errorf("NewS3StorageClient() = %v, want %v", err, errExcluded)
	}
}
