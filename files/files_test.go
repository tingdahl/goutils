package files

import (
	"bytes"
	"context"
	"testing"

	"github.com/tingdahl/goutils/storage"
)

func setupTest(t *testing.T) {
	t.Helper()
	ResetForTesting()
	ms := storage.NewMockStorageClient()
	if err := Init(ms); err != nil {
		t.Fatalf("failed to init files repository: %v", err)
	}
}

func TestFilesClient_CRUD(t *testing.T) {
	setupTest(t)
	ctx := context.Background()

	client, err := GetFilesClient(ctx, "tenants/42")
	if err != nil {
		t.Fatalf("failed to get files client: %v", err)
	}

	// 1. Save file
	dummyData := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}
	meta, err := client.SaveFile(ctx, "logo.png", "image/png", dummyData, &FileReferenceProto{
		Type:     ReferenceType_CHANNEL,
		EntityId: "main-stage",
	}, "user_123")
	if err != nil {
		t.Fatalf("failed to save file: %v", err)
	}

	if meta.FileId != 1 {
		t.Fatalf("expected file_id 1, got %d", meta.FileId)
	}
	if meta.Name != "logo.png" {
		t.Fatalf("expected name logo.png, got %s", meta.Name)
	}

	// 2. Read content
	readData, err := client.ReadFileContent(ctx, meta.FileId)
	if err != nil {
		t.Fatalf("failed to read file content: %v", err)
	}
	if !bytes.Equal(readData, dummyData) {
		t.Fatalf("read data does not match original data")
	}

	// 3. Get metadata
	gotMeta := client.GetFile(meta.FileId)
	if gotMeta == nil {
		t.Fatalf("expected to get metadata for file %d, got nil", meta.FileId)
	}
	if gotMeta.Reference == nil || gotMeta.Reference.EntityId != "main-stage" {
		t.Fatalf("unexpected reference: %+v", gotMeta.Reference)
	}

	// 4. Query by reference
	matches := client.GetFilesByReference(ReferenceType_CHANNEL, 0, "main-stage")
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}

	// 5. Delete file
	if err := client.DeleteFile(ctx, meta.FileId); err != nil {
		t.Fatalf("failed to delete file: %v", err)
	}

	if client.GetFile(meta.FileId) != nil {
		t.Fatalf("file metadata still exists after deletion")
	}

	_, err = client.ReadFileContent(ctx, meta.FileId)
	if err == nil {
		t.Fatalf("expected error reading deleted file, got nil")
	}
}

func TestFilesClient_PrefixIsolation(t *testing.T) {
	setupTest(t)
	ctx := context.Background()

	client1, err := GetFilesClient(ctx, "tenants/1")
	if err != nil {
		t.Fatalf("failed to get client1: %v", err)
	}

	client2, err := GetFilesClient(ctx, "tenants/2")
	if err != nil {
		t.Fatalf("failed to get client2: %v", err)
	}

	_, err = client1.SaveFile(ctx, "t1.png", "image/png", []byte("t1"), nil, "user1")
	if err != nil {
		t.Fatalf("failed to save in tenant 1: %v", err)
	}

	if len(client1.GetFiles()) != 1 {
		t.Fatalf("expected 1 file in tenant 1")
	}
	if len(client2.GetFiles()) != 0 {
		t.Fatalf("expected 0 files in tenant 2, got %d", len(client2.GetFiles()))
	}
}
