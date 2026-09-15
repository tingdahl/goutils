package docstore

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/tingdahl/goutils/storage"
	"github.com/tingdahl/goutils/version"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type mockDocStorage struct {
	mu            sync.Mutex
	data          map[string][]byte
	revisions     map[string]string
	conflictCount int
}

func newMockDocStorage() *mockDocStorage {
	return &mockDocStorage{
		data:      make(map[string][]byte),
		revisions: make(map[string]string),
	}
}

func (m *mockDocStorage) GetCurrentRevision(ctx context.Context, object string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.revisions[object], nil
}

func (m *mockDocStorage) WriteObject(ctx context.Context, object string, data []byte) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[object] = data
	m.revisions[object] = "rev-1"
	return "rev-1", nil
}

func (m *mockDocStorage) WriteRawObject(ctx context.Context, object string, data []byte) (string, error) {
	return m.WriteObject(ctx, object, data)
}

func (m *mockDocStorage) WriteObjectIfRevisionMatch(ctx context.Context, object string, data []byte, revision string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.conflictCount > 0 {
		m.conflictCount--
		// Simulate a concurrent writer that advanced the revision
		m.revisions[object] = "rev-concurrent"
		return "", storage.RevisionWriteError
	}

	current := m.revisions[object]
	if current != revision {
		return "", storage.RevisionWriteError
	}
	m.data[object] = data
	nextRev := current + "-next"
	if current == "" {
		nextRev = "rev-1"
	}
	m.revisions[object] = nextRev
	return nextRev, nil
}

func (m *mockDocStorage) ReadRawObject(ctx context.Context, object string) ([]byte, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, exists := m.data[object]
	if !exists {
		return nil, "", errors.New("storage: object doesn't exist")
	}
	return data, m.revisions[object], nil
}

func (m *mockDocStorage) ReadObject(ctx context.Context, object string) ([]byte, string, error) {
	return m.ReadRawObject(ctx, object)
}

func (m *mockDocStorage) GetObjectLink(ctx context.Context, object string, duration int, IPAddress string) (string, error) {
	return "", nil
}
func (m *mockDocStorage) GetUploadLink(ctx context.Context, object string, duration int, contentType string) (string, error) {
	return "", nil
}
func (m *mockDocStorage) DeleteObject(ctx context.Context, object string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, object)
	delete(m.revisions, object)
	return nil
}
func (m *mockDocStorage) ListPrefixes(ctx context.Context, prefix string, delimiter string) ([]string, error) {
	return nil, nil
}
func (m *mockDocStorage) ListObjects(ctx context.Context, prefix string) ([]storage.StorageObject, error) {
	return nil, nil
}

type testDocClient struct {
	msg                  wrapperspb.StringValue
	schemaMinor          int32
	lastModifiedByCommit string
}

func (tc *testDocClient) ReadFromProto(data []byte) error {
	if data == nil {
		tc.msg.Reset()
		return nil
	}
	return proto.Unmarshal(data, &tc.msg)
}

func (tc *testDocClient) GetSchemaMinorVersion() int32 {
	return tc.schemaMinor
}

func (tc *testDocClient) StampVersion(msg proto.Message, schemaMinor int32, commit string) {
	tc.schemaMinor = schemaMinor
	tc.lastModifiedByCommit = commit
}

func TestDocstore_VersionGuard_AllowsSameOrOlderMinor(t *testing.T) {
	store := newMockDocStorage()
	initialData, _ := proto.Marshal(wrapperspb.String("hello"))
	store.data["doc.v1.pb"] = initialData
	store.revisions["doc.v1.pb"] = "rev-0"

	tc := &testDocClient{schemaMinor: 0}

	doc := &Document{
		Client:               tc,
		Storage:              store,
		BucketName:           "test-bucket",
		ObjectName:           "doc.v1.pb",
		SupportedSchemaMinor: 1,
		ProtoMsg:             &tc.msg,
	}

	err := doc.Update(context.Background(), func(msg proto.Message) error {
		if s, ok := msg.(*wrapperspb.StringValue); ok {
			s.Value = "updated"
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Expected update to succeed: %v", err)
	}

	if tc.schemaMinor != 1 {
		t.Errorf("Expected SchemaMinorVersion stamped to 1, got %d", tc.schemaMinor)
	}
	if tc.lastModifiedByCommit != version.GetGitCommit() {
		t.Errorf("Expected commit stamped to %q, got %q", version.GetGitCommit(), tc.lastModifiedByCommit)
	}
	if tc.msg.Value != "updated" {
		t.Errorf("Expected msg.Value = 'updated', got %q", tc.msg.Value)
	}
}

func TestDocstore_VersionGuard_BlocksNewerMinor(t *testing.T) {
	store := newMockDocStorage()
	initialData, _ := proto.Marshal(wrapperspb.String("hello"))
	store.data["doc.v1.pb"] = initialData
	store.revisions["doc.v1.pb"] = "rev-0"

	// Existing data was written with minor version 2
	tc := &testDocClient{schemaMinor: 2}

	// This runtime only supports minor version 1
	doc := &Document{
		Client:               tc,
		Storage:              store,
		BucketName:           "test-bucket",
		ObjectName:           "doc.v1.pb",
		SupportedSchemaMinor: 1,
		ProtoMsg:             &tc.msg,
	}

	err := doc.Update(context.Background(), func(msg proto.Message) error {
		return nil
	})
	if !errors.Is(err, ErrNewerSchemaVersionWriteForbidden) {
		t.Fatalf("Expected ErrNewerSchemaVersionWriteForbidden, got: %v", err)
	}
}

func TestDocstore_OptimisticConcurrencyRetry(t *testing.T) {
	store := newMockDocStorage()
	initialData, _ := proto.Marshal(wrapperspb.String("initial"))
	store.data["doc.v1.pb"] = initialData
	store.revisions["doc.v1.pb"] = "rev-0"
	store.conflictCount = 2 // will fail twice with RevisionWriteError before succeeding

	tc := &testDocClient{schemaMinor: 1}
	doc := &Document{
		Client:               tc,
		Storage:              store,
		BucketName:           "test-bucket",
		ObjectName:           "doc.v1.pb",
		SupportedSchemaMinor: 1,
		ProtoMsg:             &tc.msg,
	}

	err := doc.Update(context.Background(), func(msg proto.Message) error {
		if s, ok := msg.(*wrapperspb.StringValue); ok {
			s.Value = "eventual-success"
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Expected update to succeed after retries, got: %v", err)
	}
}

func TestDocstore_NewDocumentInitialCreation(t *testing.T) {
	store := newMockDocStorage()
	tc := &testDocClient{schemaMinor: 0}

	doc := &Document{
		Client:               tc,
		Storage:              store,
		BucketName:           "test-bucket",
		ObjectName:           "new-doc.v1.pb",
		SupportedSchemaMinor: 1,
		ProtoMsg:             &tc.msg,
	}

	err := doc.Update(context.Background(), func(msg proto.Message) error {
		if s, ok := msg.(*wrapperspb.StringValue); ok {
			s.Value = "created"
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Expected creation of new document to succeed, got: %v", err)
	}
	if tc.msg.Value != "created" {
		t.Errorf("Expected tc.msg.Value = 'created', got %q", tc.msg.Value)
	}

	// UpdateFromStorage
	if err := doc.UpdateFromStorage(); err != nil {
		t.Fatalf("UpdateFromStorage failed: %v", err)
	}
}
