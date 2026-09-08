//go:build !exclude_s3

package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/tingdahl/goutils/storage"
)

func TestS3Init(t *testing.T) {
	err := Init("eu-west-1", "https://s3.example.com")
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// S3 constructor was registered
	client, err := storage.NewStorageClient()
	// NewStorageClient should call NewS3StorageClient which loads config without error (may succeed even without AWS creds)
	if err == nil && client == nil {
		t.Fatal("expected non-nil client when err is nil")
	}
}

func TestS3StorageLifecycle(t *testing.T) {
	bucketName := os.Getenv(EnvS3Bucket)
	if bucketName == "" {
		t.Skip("Skipping integration tests: S3_BUCKET environment variable is not set")
	}

	region := os.Getenv(EnvS3Region)
	if region == "" {
		region = "eu-west-1"
	}

	endpoint := os.Getenv(EnvS3Endpoint)

	ctx := context.Background()
	client, err := NewS3StorageClient(ctx, region, endpoint)
	if err != nil {
		t.Fatalf("Failed to create S3 storage client: %v", err)
	}

	objectName := fmt.Sprintf("integration_test_%d.txt", time.Now().UnixNano())
	data1 := []byte("hello integration test")
	data2 := []byte("hello updated test")

	// 1. WriteObject
	revision1, err := client.WriteObject(ctx, bucketName, objectName, data1)
	if err != nil {
		t.Fatalf("WriteObject failed: %v", err)
	}
	if revision1 == "" {
		t.Fatalf("WriteObject returned empty revision")
	}

	// 2. ReadObject
	readData, readRevision, err := client.ReadObject(ctx, bucketName, objectName)
	if err != nil {
		t.Fatalf("ReadObject failed: %v", err)
	}
	if readRevision != revision1 {
		t.Fatalf("ReadObject returned mismatched revision. Expected %s, got %s", revision1, readRevision)
	}
	if !bytes.Equal(readData, data1) {
		t.Fatalf("ReadObject content mismatch. Expected %s, got %s", data1, readData)
	}

	// 3. GetCurrentRevision
	currentRev, err := client.GetCurrentRevision(ctx, bucketName, objectName)
	if err != nil {
		t.Fatalf("GetCurrentRevision failed: %v", err)
	}
	if currentRev != revision1 {
		t.Fatalf("GetCurrentRevision mismatch. Expected %s, got %s", revision1, currentRev)
	}

	// 4. WriteObjectIfRevisionMatch (Failure Case)
	badRevision := revision1 + "_bad"
	_, err = client.WriteObjectIfRevisionMatch(ctx, bucketName, objectName, data2, badRevision)
	if err == nil {
		t.Fatal("WriteObjectIfRevisionMatch should have failed with bad revision, but it succeeded")
	}
	if !errors.Is(err, storage.RevisionWriteError) && err != storage.RevisionWriteError {
		t.Fatalf("Expected RevisionWriteError, but got: %v", err)
	}

	// 5. WriteObjectIfRevisionMatch (Success Case)
	revision2, err := client.WriteObjectIfRevisionMatch(ctx, bucketName, objectName, data2, revision1)
	if err != nil {
		t.Fatalf("WriteObjectIfRevisionMatch failed when expected to succeed: %v", err)
	}
	if revision2 == revision1 {
		t.Fatalf("WriteObjectIfRevisionMatch did not alter revision.")
	}

	// 6. GetObjectLink (Signed URL)
	url, err := client.GetObjectLink(ctx, bucketName, objectName, 3600, "")
	if err != nil {
		t.Fatalf("GetObjectLink failed: %v", err)
	}
	if url == "" {
		t.Fatal("GetObjectLink returned empty URL")
	}

	// Clean up
	defer func() {
		_ = client.DeleteObject(ctx, bucketName, objectName)
	}()
}
