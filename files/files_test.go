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

	// 2b. Get signed URL
	signedURL, err := client.GetSignedURL(ctx, meta.FileId, 60)
	if err != nil {
		t.Fatalf("failed to get signed URL: %v", err)
	}
	expectedURL := "https://mock-storage/tenants/42/files/1"
	if signedURL != expectedURL {
		t.Fatalf("expected signed URL %s, got %s", expectedURL, signedURL)
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

func TestFilesClient_EdgeCasesAndErrors(t *testing.T) {
	ctx := context.Background()

	// 1. Uninitialized GetFilesClient
	ResetForTesting()
	if _, err := GetFilesClient(ctx, "prefix"); err == nil {
		t.Error("expected error when calling GetFilesClient before Init")
	}

	// 2. Init with nil store
	if err := Init(nil); err == nil {
		t.Error("expected error when Init called with nil store")
	}

	setupTest(t)

	// 3. Object naming helpers
	if got := FilesCatalogObjectName(""); got != "files.v1.pb.zst" {
		t.Errorf("FilesCatalogObjectName('') = %q, want files.v1.pb.zst", got)
	}
	if got := FilesCatalogObjectName("/abc/def/"); got != "abc/def/files.v1.pb.zst" {
		t.Errorf("FilesCatalogObjectName('/abc/def/') = %q, want abc/def/files.v1.pb.zst", got)
	}
	if got := FileRawObjectName("", 5); got != "files/5" {
		t.Errorf("FileRawObjectName('', 5) = %q, want files/5", got)
	}
	if got := FileRawObjectName("/abc/", 5); got != "abc/files/5" {
		t.Errorf("FileRawObjectName('/abc/', 5) = %q, want abc/files/5", got)
	}

	client, err := GetFilesClient(ctx, "test-prefix")
	if err != nil {
		t.Fatalf("failed to get client: %v", err)
	}
	if client.Prefix() != "test-prefix" {
		t.Errorf("expected prefix test-prefix, got %q", client.Prefix())
	}

	// 4. Schema versioning & proto helpers
	if client.GetSchemaMinorVersion() != SchemaMinor {
		t.Errorf("expected schema minor %d, got %d", SchemaMinor, client.GetSchemaMinorVersion())
	}
	if err := client.ReadFromProto(nil); err != nil {
		t.Errorf("ReadFromProto(nil) failed: %v", err)
	}
	var cat FilesCatalogProto
	client.StampVersion(&cat, 2, "commit-hash")
	if cat.SchemaMinorVersion != 2 || cat.LastModifiedByCommit != "commit-hash" {
		t.Errorf("StampVersion failed: %+v", &cat)
	}

	// 5. GetSignedURL validations
	if _, err := client.GetSignedURL(ctx, 0, 60); err == nil {
		t.Error("expected error for fileId <= 0")
	}
	urlDefaultDuration, err := client.GetSignedURL(ctx, 1, 0)
	if err != nil || urlDefaultDuration == "" {
		t.Errorf("GetSignedURL with default duration failed: %v", err)
	}

	// 6. SetReference on non-existent file
	err = client.SetReference(ctx, 999, &FileReferenceProto{Type: ReferenceType_CHANNEL})
	if err != ErrFileNotFound {
		t.Errorf("expected ErrFileNotFound, got: %v", err)
	}

	// 7. DeleteFile on non-existent file
	err = client.DeleteFile(ctx, 999)
	if err != ErrFileNotFound {
		t.Errorf("expected ErrFileNotFound for missing delete, got: %v", err)
	}

	// 8. Add file without reference and update reference
	meta, err := client.AddFile(ctx, &FileMetadataProto{Name: "unref.png"}, "user1")
	if err != nil {
		t.Fatalf("AddFile failed: %v", err)
	}
	if meta.Reference == nil || meta.Reference.Type != ReferenceType_UNASSIGNED {
		t.Errorf("expected UNASSIGNED default reference")
	}

	newRef := &FileReferenceProto{
		Type:     ReferenceType_TRANSACTION,
		Id:       10,
		EntityId: "entity-10",
	}
	if err := client.SetReference(ctx, meta.FileId, newRef); err != nil {
		t.Fatalf("SetReference failed: %v", err)
	}
	updated := client.GetFile(meta.FileId)
	if updated.Reference.Type != ReferenceType_TRANSACTION || updated.Reference.Id != 10 {
		t.Errorf("unexpected updated reference: %+v", updated.Reference)
	}

	// 9. Query files by reference variations
	matches := client.GetFilesByReference(ReferenceType_TRANSACTION, 10, "entity-10")
	if len(matches) != 1 {
		t.Errorf("expected 1 match, got %d", len(matches))
	}
	matches = client.GetFilesByReference(ReferenceType_TRANSACTION, 99, "entity-10")
	if len(matches) != 0 {
		t.Errorf("expected 0 matches for wrong id, got %d", len(matches))
	}
	matches = client.GetFilesByReference(ReferenceType_TRANSACTION, 10, "wrong-entity")
	if len(matches) != 0 {
		t.Errorf("expected 0 matches for wrong entityId, got %d", len(matches))
	}
	matches = client.GetFilesByReference(ReferenceType_CHANNEL, 0, "")
	if len(matches) != 0 {
		t.Errorf("expected 0 matches for channel type, got %d", len(matches))
	}

	// 10. GetFile with non-existent ID
	if client.GetFile(9999) != nil {
		t.Error("expected nil for non-existent file ID")
	}
}

func TestFiles_ProtobufGetters(t *testing.T) {
	ref := &FileReferenceProto{
		Type:     ReferenceType_TRANSACTION,
		Id:       42,
		EntityId: "ent-1",
	}
	_ = ref.GetType()
	_ = ref.GetId()
	_ = ref.GetEntityId()
	_ = ref.String()
	_, _ = ref.Descriptor()
	_ = ref.ProtoReflect()
	ref.ProtoMessage()

	meta := &FileMetadataProto{
		FileId:          1,
		Name:            "test.png",
		MimeType:        "image/png",
		SizeBytes:       100,
		CreatedAtUnixMs: 123,
		UpdatedAtUnixMs: 456,
		CreatedBy:       "user-1",
		Thumbnail:       []byte("thumb"),
		Reference:       ref,
	}
	_ = meta.GetFileId()
	_ = meta.GetName()
	_ = meta.GetMimeType()
	_ = meta.GetSizeBytes()
	_ = meta.GetCreatedAtUnixMs()
	_ = meta.GetUpdatedAtUnixMs()
	_ = meta.GetCreatedBy()
	_ = meta.GetThumbnail()
	_ = meta.GetReference()
	_ = meta.String()
	_, _ = meta.Descriptor()
	_ = meta.ProtoReflect()
	meta.ProtoMessage()

	cat := &FilesCatalogProto{
		SchemaMinorVersion:   1,
		LastModifiedByCommit: "commit",
		Files:                []*FileMetadataProto{meta},
	}
	_ = cat.GetSchemaMinorVersion()
	_ = cat.GetLastModifiedByCommit()
	_ = cat.GetFiles()
	_ = cat.String()
	_, _ = cat.Descriptor()
	_ = cat.ProtoReflect()
	cat.ProtoMessage()

	var rType ReferenceType = ReferenceType_CHANNEL
	_ = rType.Enum()
	_ = rType.String()
	_ = rType.Descriptor()
	_ = rType.Type()
	_ = rType.Number()
	_, _ = rType.EnumDescriptor()

	// Nil getters
	var nilRef *FileReferenceProto
	_ = nilRef.GetType()
	_ = nilRef.GetId()
	_ = nilRef.GetEntityId()

	var nilMeta *FileMetadataProto
	_ = nilMeta.GetFileId()
	_ = nilMeta.GetName()
	_ = nilMeta.GetMimeType()
	_ = nilMeta.GetSizeBytes()
	_ = nilMeta.GetCreatedAtUnixMs()
	_ = nilMeta.GetUpdatedAtUnixMs()
	_ = nilMeta.GetCreatedBy()
	_ = nilMeta.GetThumbnail()
	_ = nilMeta.GetReference()

	var nilCat *FilesCatalogProto
	_ = nilCat.GetSchemaMinorVersion()
	_ = nilCat.GetLastModifiedByCommit()
	_ = nilCat.GetFiles()
}
