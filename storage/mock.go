package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// MockStorageClient provides a thread-safe, in-memory implementation of StorageClient for testing.
type MockStorageClient struct {
	mu        sync.RWMutex
	objects   map[string][]byte
	revisions map[string]int
}

// NewMockStorageClient creates a new MockStorageClient instance.
func NewMockStorageClient() *MockStorageClient {
	return &MockStorageClient{
		objects:   make(map[string][]byte),
		revisions: make(map[string]int),
	}
}

// GetCurrentRevision returns the current revision of the object.
func (m *MockStorageClient) GetCurrentRevision(ctx context.Context, object string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rev, ok := m.revisions[object]
	if !ok {
		return "", nil
	}
	return fmt.Sprintf("rev-%d", rev), nil
}

// WriteObject writes data and increments the revision.
func (m *MockStorageClient) WriteObject(ctx context.Context, object string, data []byte) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.revisions[object]++
	m.objects[object] = data
	return fmt.Sprintf("rev-%d", m.revisions[object]), nil
}

// WriteRawObject writes raw uncompressed data.
func (m *MockStorageClient) WriteRawObject(ctx context.Context, object string, data []byte) (string, error) {
	return m.WriteObject(ctx, object, data)
}

// WriteObjectIfRevisionMatch conditionally writes data if the revision matches.
func (m *MockStorageClient) WriteObjectIfRevisionMatch(ctx context.Context, object string, data []byte, revision string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	currentRev := m.revisions[object]
	expectedRevStr := ""
	if currentRev > 0 {
		expectedRevStr = fmt.Sprintf("rev-%d", currentRev)
	}

	if revision != expectedRevStr {
		return "", RevisionWriteError
	}

	m.revisions[object]++
	m.objects[object] = data
	return fmt.Sprintf("rev-%d", m.revisions[object]), nil
}

// ReadObject reads and returns stored data for the object.
func (m *MockStorageClient) ReadObject(ctx context.Context, object string) ([]byte, string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	data, ok := m.objects[object]
	if !ok {
		return nil, "", errors.New("storage: object doesn't exist")
	}
	return data, fmt.Sprintf("rev-%d", m.revisions[object]), nil
}

// ReadRawObject reads raw bytes without decompression.
func (m *MockStorageClient) ReadRawObject(ctx context.Context, object string) ([]byte, string, error) {
	return m.ReadObject(ctx, object)
}

// GetObjectLink returns a signed URL.
func (m *MockStorageClient) GetObjectLink(ctx context.Context, object string, duration int, ip string) (string, error) {
	return fmt.Sprintf("https://mock-storage/%s", object), nil
}

// GetUploadLink returns a mock upload URL.
func (m *MockStorageClient) GetUploadLink(ctx context.Context, object string, duration int, contentType string) (string, error) {
	return fmt.Sprintf("https://mock-storage/upload/%s", object), nil
}

// DeleteObject removes the object from in-memory storage.
func (m *MockStorageClient) DeleteObject(ctx context.Context, object string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, object)
	delete(m.revisions, object)
	return nil
}

// ListPrefixes returns prefixes.
func (m *MockStorageClient) ListPrefixes(ctx context.Context, prefix, delimiter string) ([]string, error) {
	return nil, nil
}

// ListObjects returns objects matching prefix.
func (m *MockStorageClient) ListObjects(ctx context.Context, prefix string) ([]StorageObject, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []StorageObject
	for k := range m.objects {
		if strings.HasPrefix(k, prefix) {
			result = append(result, StorageObject{
				Key:          k,
				LastModified: time.Now(),
			})
		}
	}
	return result, nil
}
