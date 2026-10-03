package storage

import (
	"context"
	"errors"
	"testing"
	"time"
)

type mockStorageClient struct {
	objects map[string][]byte
}

func (m *mockStorageClient) GetCurrentRevision(ctx context.Context, object string) (string, error) {
	return "rev-1", nil
}
func (m *mockStorageClient) WriteObject(ctx context.Context, object string, data []byte) (string, error) {
	m.objects[object] = data
	return "rev-1", nil
}
func (m *mockStorageClient) WriteRawObject(ctx context.Context, object string, data []byte) (string, error) {
	m.objects[object] = data
	return "rev-1", nil
}
func (m *mockStorageClient) WriteObjectIfRevisionMatch(ctx context.Context, object string, data []byte, revision string) (string, error) {
	if revision != "rev-1" {
		return "", RevisionWriteError
	}
	m.objects[object] = data
	return "rev-2", nil
}
func (m *mockStorageClient) ReadObject(ctx context.Context, object string) ([]byte, string, error) {
	data, ok := m.objects[object]
	if !ok {
		return nil, "", errors.New("not found")
	}
	return data, "rev-1", nil
}
func (m *mockStorageClient) ReadRawObject(ctx context.Context, object string) ([]byte, string, error) {
	return m.ReadObject(ctx, object)
}
func (m *mockStorageClient) GetObjectLink(ctx context.Context, object string, duration int, IPAddress string) (string, error) {
	return "https://signed.example.com/link", nil
}
func (m *mockStorageClient) GetUploadLink(ctx context.Context, object string, duration int, contentType string) (string, error) {
	return "https://signed.example.com/upload", nil
}
func (m *mockStorageClient) DeleteObject(ctx context.Context, object string) error {
	delete(m.objects, object)
	return nil
}
func (m *mockStorageClient) ListPrefixes(ctx context.Context, prefix string, delimiter string) ([]string, error) {
	return []string{"prefix1/"}, nil
}
func (m *mockStorageClient) ListObjects(ctx context.Context, prefix string) ([]StorageObject, error) {
	return []StorageObject{{Key: "obj1", LastModified: time.Now()}}, nil
}

func TestStorageHelpers(t *testing.T) {
	if !IsZstdKey("accounting.pb.zst") {
		t.Errorf("Expected accounting.pb.zst to be recognized as Zstd key")
	}
	if !IsZstdKey("path/to/data.json.zst") {
		t.Errorf("Expected path/to/data.json.zst to be recognized as Zstd key")
	}
	if IsZstdKey("accounting.pb") {
		t.Errorf("Did not expect accounting.pb to be recognized as Zstd key")
	}

	tests := []struct {
		key      string
		expected string
	}{
		{"accounting.pb", ContentTypeApplicationProtobuf},
		{"accounting.pb.zst", ContentTypeApplicationProtobuf},
		{"accounting.pb.br", ContentTypeApplicationProtobuf},
		{"config.json", ContentTypeApplicationJSON},
		{"config.json.zst", ContentTypeApplicationJSON},
		{"receipt.pdf", "application/pdf"},
		{"receipt.pdf.zst", "application/pdf"},
		{"photo.png", "image/png"},
		{"photo.jpg", "image/jpeg"},
		{"index.html", ContentTypeTextHTML},
		{"notes.txt", ContentTypeTextPlain},
		{"app.js", ContentTypeApplicationJavaScript},
		{"module.mjs", ContentTypeApplicationJavaScript},
		{"style.css", "text/css"},
		{"icon.svg", "image/svg+xml"},
		{"wasm.wasm", "application/wasm"},
		{"unknown.bin", ContentTypeApplicationOctetStream},
	}

	for _, tt := range tests {
		got := ContentTypeFromKey(tt.key)
		if got != tt.expected {
			t.Errorf("ContentTypeFromKey(%q) = %q, expected %q", tt.key, got, tt.expected)
		}
	}
}

func TestStorageConstructorAndHealth(t *testing.T) {
	SetStorageConstructor(nil)
	_, err := NewStorageClient()
	if err == nil {
		t.Error("expected error from uninitialized NewStorageClient")
	}
	if err := CheckHealth(context.Background()); err == nil {
		t.Error("expected CheckHealth error when constructor is nil")
	}

	var capturedOpts map[string]string
	mockClient := &mockStorageClient{objects: make(map[string][]byte)}
	SetStorageConstructor(func(opts map[string]string) (StorageClient, error) {
		capturedOpts = opts
		return mockClient, nil
	})

	client, err := NewStorageClient()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if len(capturedOpts) != 0 {
		t.Errorf("expected empty opts, got %v", capturedOpts)
	}
	if err := CheckHealth(context.Background()); err != nil {
		t.Errorf("CheckHealth failed: %v", err)
	}

	client2, err := NewStorageClient(map[string]string{"S3_BUCKET": "custom-bucket"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client2 == nil {
		t.Fatal("expected non-nil client")
	}
	if capturedOpts["S3_BUCKET"] != "custom-bucket" {
		t.Errorf("expected custom-bucket, got %q", capturedOpts["S3_BUCKET"])
	}

	// Multiple opts merging
	_, _ = NewStorageClient(map[string]string{"A": "1"}, map[string]string{"B": "2"})
	if capturedOpts["A"] != "1" || capturedOpts["B"] != "2" {
		t.Errorf("expected merged opts, got %v", capturedOpts)
	}

	// Constructor error
	SetStorageConstructor(func(opts map[string]string) (StorageClient, error) {
		return nil, errors.New("init error")
	})
	if _, err := NewStorageClient(); err == nil {
		t.Error("expected error from failing constructor")
	}
	if err := CheckHealth(context.Background()); err == nil {
		t.Error("expected CheckHealth error when constructor fails")
	}

	// Constructor returns nil client
	SetStorageConstructor(func(opts map[string]string) (StorageClient, error) {
		return nil, nil
	})
	if err := CheckHealth(context.Background()); err == nil {
		t.Error("expected CheckHealth error when constructor returns nil client")
	}
}

func TestStorageHelpers_Brotli(t *testing.T) {
	if !IsBrotliKey("test.pb.br") {
		t.Errorf("expected test.pb.br to be recognized as Brotli key")
	}
	if IsBrotliKey("test.pb") {
		t.Errorf("did not expect test.pb to be recognized as Brotli key")
	}
}

func TestMockStorageClient_Comprehensive(t *testing.T) {
	ctx := context.Background()
	m := NewMockStorageClient()

	// Initial revision for missing object
	rev, err := m.GetCurrentRevision(ctx, "non-existent")
	if err != nil || rev != "" {
		t.Fatalf("expected empty revision for missing object, got: %q, err: %v", rev, err)
	}

	// Read non-existent object
	_, _, err = m.ReadObject(ctx, "non-existent")
	if err == nil {
		t.Fatal("expected error reading non-existent object")
	}
	_, _, err = m.ReadRawObject(ctx, "non-existent")
	if err == nil {
		t.Fatal("expected error reading raw non-existent object")
	}

	// Write raw object
	rev1, err := m.WriteRawObject(ctx, "data.txt", []byte("hello raw"))
	if err != nil || rev1 != "rev-1" {
		t.Fatalf("WriteRawObject failed: rev=%s, err=%v", rev1, err)
	}

	// Read object
	data, revRead, err := m.ReadObject(ctx, "data.txt")
	if err != nil || string(data) != "hello raw" || revRead != "rev-1" {
		t.Fatalf("ReadObject failed: data=%s, rev=%s, err=%v", string(data), revRead, err)
	}
	dataRaw, revRaw, err := m.ReadRawObject(ctx, "data.txt")
	if err != nil || string(dataRaw) != "hello raw" || revRaw != "rev-1" {
		t.Fatalf("ReadRawObject failed: data=%s, rev=%s, err=%v", string(dataRaw), revRaw, err)
	}

	// GetCurrentRevision
	currentRev, err := m.GetCurrentRevision(ctx, "data.txt")
	if err != nil || currentRev != "rev-1" {
		t.Fatalf("GetCurrentRevision failed: rev=%s, err=%v", currentRev, err)
	}

	// WriteObject
	rev2, err := m.WriteObject(ctx, "data.txt", []byte("hello updated"))
	if err != nil || rev2 != "rev-2" {
		t.Fatalf("WriteObject failed: rev=%s, err=%v", rev2, err)
	}

	// WriteObjectIfRevisionMatch - success
	rev3, err := m.WriteObjectIfRevisionMatch(ctx, "data.txt", []byte("hello v3"), "rev-2")
	if err != nil || rev3 != "rev-3" {
		t.Fatalf("WriteObjectIfRevisionMatch failed: rev=%s, err=%v", rev3, err)
	}

	// WriteObjectIfRevisionMatch - mismatch
	_, err = m.WriteObjectIfRevisionMatch(ctx, "data.txt", []byte("hello v4"), "rev-wrong")
	if !errors.Is(err, RevisionWriteError) {
		t.Fatalf("expected RevisionWriteError, got: %v", err)
	}

	// WriteObjectIfRevisionMatch - creation with empty revision
	revNew, err := m.WriteObjectIfRevisionMatch(ctx, "brand-new.txt", []byte("created"), "")
	if err != nil || revNew != "rev-1" {
		t.Fatalf("WriteObjectIfRevisionMatch creation failed: rev=%s, err=%v", revNew, err)
	}

	// URLs
	objLink, err := m.GetObjectLink(ctx, "data.txt", 3600, "")
	if err != nil || objLink != "https://mock-storage/data.txt" {
		t.Fatalf("GetObjectLink failed: %s, %v", objLink, err)
	}
	upLink, err := m.GetUploadLink(ctx, "upload.txt", 3600, "text/plain")
	if err != nil || upLink != "https://mock-storage/upload/upload.txt" {
		t.Fatalf("GetUploadLink failed: %s, %v", upLink, err)
	}

	// List
	prefixes, err := m.ListPrefixes(ctx, "", "/")
	if err != nil || prefixes != nil {
		t.Fatalf("ListPrefixes failed: %v", err)
	}
	objs, err := m.ListObjects(ctx, "data")
	if err != nil || len(objs) != 1 || objs[0].Key != "data.txt" {
		t.Fatalf("ListObjects failed: %v, %v", objs, err)
	}

	// Delete
	if err := m.DeleteObject(ctx, "data.txt"); err != nil {
		t.Fatalf("DeleteObject failed: %v", err)
	}
	_, err = m.GetCurrentRevision(ctx, "data.txt")
	if err != nil {
		t.Fatalf("expected nil error after delete, got %v", err)
	}
}
