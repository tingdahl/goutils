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
}
