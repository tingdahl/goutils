package storage

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/tingdahl/goutils/config"
)

type mockStorageClient struct {
	objects map[string][]byte
}

func (m *mockStorageClient) GetCurrentRevision(ctx context.Context, bucket string, object string) (string, error) {
	return "rev-1", nil
}
func (m *mockStorageClient) WriteObject(ctx context.Context, bucket string, object string, data []byte) (string, error) {
	m.objects[object] = data
	return "rev-1", nil
}
func (m *mockStorageClient) WriteRawObject(ctx context.Context, bucket string, object string, data []byte) (string, error) {
	m.objects[object] = data
	return "rev-1", nil
}
func (m *mockStorageClient) WriteObjectIfRevisionMatch(ctx context.Context, bucket string, object string, data []byte, revision string) (string, error) {
	if revision != "rev-1" {
		return "", RevisionWriteError
	}
	m.objects[object] = data
	return "rev-2", nil
}
func (m *mockStorageClient) ReadObject(ctx context.Context, bucket string, object string) ([]byte, string, error) {
	data, ok := m.objects[object]
	if !ok {
		return nil, "", errors.New("not found")
	}
	return data, "rev-1", nil
}
func (m *mockStorageClient) ReadRawObject(ctx context.Context, bucket string, object string) ([]byte, string, error) {
	return m.ReadObject(ctx, bucket, object)
}
func (m *mockStorageClient) GetObjectLink(ctx context.Context, bucket string, object string, duration int, IPAddress string) (string, error) {
	return "https://signed.example.com/link", nil
}
func (m *mockStorageClient) GetUploadLink(ctx context.Context, bucket string, object string, duration int, contentType string) (string, error) {
	return "https://signed.example.com/upload", nil
}
func (m *mockStorageClient) DeleteObject(ctx context.Context, bucket string, object string) error {
	delete(m.objects, object)
	return nil
}
func (m *mockStorageClient) ListPrefixes(ctx context.Context, bucket string, prefix string, delimiter string) ([]string, error) {
	return []string{"prefix1/"}, nil
}
func (m *mockStorageClient) ListObjects(ctx context.Context, bucket string, prefix string) ([]StorageObject, error) {
	return []StorageObject{{Key: "obj1", LastModified: time.Now()}}, nil
}

func TestStorageHelpers(t *testing.T) {
	if !IsBrotliKey("accounting.pb.br") {
		t.Errorf("Expected accounting.pb.br to be recognized as Brotli key")
	}
	if !IsBrotliKey("path/to/data.json.br") {
		t.Errorf("Expected path/to/data.json.br to be recognized as Brotli key")
	}
	if IsBrotliKey("accounting.pb") {
		t.Errorf("Did not expect accounting.pb to be recognized as Brotli key")
	}

	tests := []struct {
		key      string
		expected string
	}{
		{"accounting.pb", ContentTypeApplicationProtobuf},
		{"accounting.pb.br", ContentTypeApplicationProtobuf},
		{"config.json", ContentTypeApplicationJSON},
		{"config.json.br", ContentTypeApplicationJSON},
		{"receipt.pdf", "application/pdf"},
		{"receipt.pdf.br", "application/pdf"},
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
	// Uninitialized constructor
	SetStorageConstructor(nil)
	_, err := NewStorageClient()
	if err == nil {
		t.Error("expected error from uninitialized NewStorageClient")
	}
	if err := CheckHealth(context.Background()); err == nil {
		t.Error("expected CheckHealth error when constructor is nil")
	}

	// Register mock constructor
	mockClient := &mockStorageClient{objects: make(map[string][]byte)}
	SetStorageConstructor(func() (StorageClient, error) {
		return mockClient, nil
	})

	client, err := NewStorageClient()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if err := CheckHealth(context.Background()); err != nil {
		t.Errorf("CheckHealth failed: %v", err)
	}

	// Test Init() with S3_APPDATA_NAME
	BucketName = ""
	os.Setenv(config.EnvS3AppDataName, "test-bucket")
	defer os.Unsetenv(config.EnvS3AppDataName)

	if err := Init(); err != nil {
		t.Fatalf("Init() failed: %v", err)
	}
	if BucketName != "test-bucket" {
		t.Errorf("BucketName = %q, want 'test-bucket'", BucketName)
	}
}
