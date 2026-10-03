package docstore

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

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
	revSeq        int64
	writeCalls    int64
	readCalls     int64
}

func newMockDocStorage() *mockDocStorage {
	return &mockDocStorage{
		data:      make(map[string][]byte),
		revisions: make(map[string]string),
	}
}

func (m *mockDocStorage) GetCurrentRevision(ctx context.Context, object string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.revisions[object], nil
}

func (m *mockDocStorage) WriteObject(ctx context.Context, object string, data []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writeCalls++
	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)
	m.data[object] = dataCopy
	m.revSeq++
	current := m.revisions[object]
	nextRev := fmt.Sprintf("%s.rev%d", current, m.revSeq)
	if current == "" {
		nextRev = fmt.Sprintf("rev-%d", m.revSeq)
	}
	m.revisions[object] = nextRev
	return nextRev, nil
}

func (m *mockDocStorage) WriteRawObject(ctx context.Context, object string, data []byte) (string, error) {
	return m.WriteObject(ctx, object, data)
}

func (m *mockDocStorage) WriteObjectIfRevisionMatch(ctx context.Context, object string, data []byte, revision string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writeCalls++

	if m.conflictCount > 0 {
		m.conflictCount--
		// Simulate a concurrent writer that advanced the revision
		m.revSeq++
		m.revisions[object] = fmt.Sprintf("rev-concurrent-%d", m.revSeq)
		return "", storage.RevisionWriteError
	}

	current := m.revisions[object]
	if current != revision {
		return "", storage.RevisionWriteError
	}
	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)
	m.data[object] = dataCopy
	m.revSeq++
	nextRev := fmt.Sprintf("%s.rev%d", current, m.revSeq)
	if current == "" {
		nextRev = fmt.Sprintf("rev-%d", m.revSeq)
	}
	m.revisions[object] = nextRev
	return nextRev, nil
}

func (m *mockDocStorage) ReadRawObject(ctx context.Context, object string) ([]byte, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.readCalls++
	data, exists := m.data[object]
	if !exists {
		return nil, "", errors.New("storage: object doesn't exist")
	}
	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)
	return dataCopy, m.revisions[object], nil
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
	if err := ctx.Err(); err != nil {
		return err
	}
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
	mu                   sync.Mutex
	msg                  wrapperspb.StringValue
	schemaMinor          int32
	lastModifiedByCommit string
}

func (tc *testDocClient) ReadFromProto(data []byte) error {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if data == nil {
		tc.msg.Reset()
		return nil
	}
	return proto.Unmarshal(data, &tc.msg)
}

func (tc *testDocClient) GetSchemaMinorVersion() int32 {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return tc.schemaMinor
}

func (tc *testDocClient) StampVersion(msg proto.Message, schemaMinor int32, commit string) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.schemaMinor = schemaMinor
	tc.lastModifiedByCommit = commit
}

func (tc *testDocClient) StringValue() string {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return tc.msg.Value
}

type testInt64DocClient struct {
	mu                   sync.Mutex
	msg                  wrapperspb.Int64Value
	schemaMinor          int32
	lastModifiedByCommit string
}

func (tc *testInt64DocClient) ReadFromProto(data []byte) error {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if data == nil {
		tc.msg.Reset()
		return nil
	}
	return proto.Unmarshal(data, &tc.msg)
}

func (tc *testInt64DocClient) GetSchemaMinorVersion() int32 {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return tc.schemaMinor
}

func (tc *testInt64DocClient) StampVersion(msg proto.Message, schemaMinor int32, commit string) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.schemaMinor = schemaMinor
	tc.lastModifiedByCommit = commit
}

func (tc *testInt64DocClient) Value() int64 {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return tc.msg.Value
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

	if tc.GetSchemaMinorVersion() != 1 {
		t.Errorf("Expected SchemaMinorVersion stamped to 1, got %d", tc.GetSchemaMinorVersion())
	}
	if tc.lastModifiedByCommit != version.GetGitCommit() {
		t.Errorf("Expected commit stamped to %q, got %q", version.GetGitCommit(), tc.lastModifiedByCommit)
	}
	if tc.StringValue() != "updated" {
		t.Errorf("Expected msg.Value = 'updated', got %q", tc.StringValue())
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
	if tc.StringValue() != "created" {
		t.Errorf("Expected tc.msg.Value = 'created', got %q", tc.StringValue())
	}

	// UpdateFromStorage
	if err := doc.UpdateFromStorage(); err != nil {
		t.Fatalf("UpdateFromStorage failed: %v", err)
	}
}

func TestDocstore_ConcurrentUpdate_ContentionRace(t *testing.T) {
	const numGoroutines = 40
	store := newMockDocStorage()
	initialData, _ := proto.Marshal(wrapperspb.Int64(0))
	store.data["counter.v1.pb"] = initialData
	store.revisions["counter.v1.pb"] = "rev-0"

	ctx := context.Background()

	// 1. Parallel goroutines calling doc.Update on the same Document instance
	tc := &testInt64DocClient{schemaMinor: 1}
	sharedDoc := &Document{
		Client:               tc,
		Storage:              store,
		BucketName:           "test-bucket",
		ObjectName:           "counter.v1.pb",
		SupportedSchemaMinor: 1,
		ProtoMsg:             &tc.msg,
	}

	var wg sync.WaitGroup
	wg.Add(numGoroutines)
	start := make(chan struct{})

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			<-start
			err := sharedDoc.Update(ctx, func(msg proto.Message) error {
				if v, ok := msg.(*wrapperspb.Int64Value); ok {
					v.Value++
				}
				return nil
			})
			if err != nil {
				t.Errorf("sharedDoc.Update failed: %v", err)
			}
		}()
	}

	close(start)
	wg.Wait()

	if tc.Value() != int64(numGoroutines) {
		t.Fatalf("expected sharedDoc counter %d, got %d", numGoroutines, tc.Value())
	}

	// 2. Parallel goroutines across distinct Document handles
	// competing with true optimistic revision collisions and retries on storage
	store2 := newMockDocStorage()
	init2, _ := proto.Marshal(wrapperspb.Int64(0))
	store2.data["multi-handle-counter.v1.pb"] = init2
	store2.revisions["multi-handle-counter.v1.pb"] = "rev-0"

	var wg2 sync.WaitGroup
	wg2.Add(numGoroutines)
	start2 := make(chan struct{})

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg2.Done()
			localClient := &testInt64DocClient{schemaMinor: 1}
			localDoc := &Document{
				Client:               localClient,
				Storage:              store2,
				BucketName:           "test-bucket",
				ObjectName:           "multi-handle-counter.v1.pb",
				SupportedSchemaMinor: 1,
				ProtoMsg:             &localClient.msg,
			}
			<-start2
			err := localDoc.Update(ctx, func(msg proto.Message) error {
				if v, ok := msg.(*wrapperspb.Int64Value); ok {
					v.Value++
				}
				return nil
			})
			if err != nil {
				t.Errorf("concurrent localDoc.Update failed: %v", err)
			}
		}()
	}

	close(start2)
	wg2.Wait()

	raw, _, err := store2.ReadObject(ctx, "multi-handle-counter.v1.pb")
	if err != nil {
		t.Fatalf("failed to read final object: %v", err)
	}
	var finalVal wrapperspb.Int64Value
	if err := proto.Unmarshal(raw, &finalVal); err != nil {
		t.Fatalf("failed to unmarshal final object: %v", err)
	}
	if finalVal.Value != int64(numGoroutines) {
		t.Fatalf("expected final storage counter %d, got %d", numGoroutines, finalVal.Value)
	}
}

func TestDocstore_ConcurrentInitialCreationRace(t *testing.T) {
	const numGoroutines = 10
	store := newMockDocStorage() // document does NOT exist initially

	ctx := context.Background()
	var wg sync.WaitGroup
	wg.Add(numGoroutines)
	start := make(chan struct{})

	errs := make([]error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		idx := i
		go func() {
			defer wg.Done()
			client := &testInt64DocClient{schemaMinor: 1}
			doc := &Document{
				Client:               client,
				Storage:              store,
				BucketName:           "test-bucket",
				ObjectName:           "uncreated-counter.v1.pb",
				SupportedSchemaMinor: 1,
				ProtoMsg:             &client.msg,
			}

			<-start
			errs[idx] = doc.Update(ctx, func(msg proto.Message) error {
				if v, ok := msg.(*wrapperspb.Int64Value); ok {
					v.Value++
				}
				return nil
			})
		}()
	}

	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d failed to create/update: %v", i, err)
		}
	}

	raw, rev, err := store.ReadObject(ctx, "uncreated-counter.v1.pb")
	if err != nil {
		t.Fatalf("failed to read created object: %v", err)
	}
	var finalVal wrapperspb.Int64Value
	if err := proto.Unmarshal(raw, &finalVal); err != nil {
		t.Fatalf("failed to unmarshal created object: %v", err)
	}
	if finalVal.Value != int64(numGoroutines) {
		t.Fatalf("expected counter %d after creation race, got %d", numGoroutines, finalVal.Value)
	}
	if rev == "" {
		t.Fatalf("expected non-empty revision after creation, got empty")
	}
}

func TestDocstore_UpdateFnError_AbortsWithoutWrite(t *testing.T) {
	store := newMockDocStorage()
	initialData, _ := proto.Marshal(wrapperspb.Int64(100))
	store.data["account.v1.pb"] = initialData
	store.revisions["account.v1.pb"] = "rev-1"

	tc := &testInt64DocClient{schemaMinor: 1}
	doc := &Document{
		Client:               tc,
		Storage:              store,
		BucketName:           "test-bucket",
		ObjectName:           "account.v1.pb",
		SupportedSchemaMinor: 1,
		ProtoMsg:             &tc.msg,
	}

	// Initial load
	if err := doc.DoLoad(); err != nil {
		t.Fatalf("initial DoLoad failed: %v", err)
	}
	if tc.Value() != 100 {
		t.Fatalf("expected initial value 100, got %d", tc.Value())
	}

	writeCallsBefore := store.writeCalls
	errDomain := errors.New("insufficient balance")

	err := doc.Update(context.Background(), func(msg proto.Message) error {
		if v, ok := msg.(*wrapperspb.Int64Value); ok {
			v.Value -= 500 // Would result in negative balance
		}
		return errDomain
	})

	if !errors.Is(err, errDomain) {
		t.Fatalf("expected %v, got: %v", errDomain, err)
	}

	// Verify no write was attempted
	if store.writeCalls != writeCallsBefore {
		t.Fatalf("expected 0 writes to storage, got %d", store.writeCalls-writeCallsBefore)
	}

	// Verify in-memory client and doc state remains unchanged
	if tc.Value() != 100 {
		t.Fatalf("expected in-memory value to remain 100, got %d", tc.Value())
	}
	if doc.Revision != "rev-1" {
		t.Fatalf("expected doc.Revision to remain rev-1, got %s", doc.Revision)
	}

	// Verify storage data remains unchanged
	raw, rev, _ := store.ReadObject(context.Background(), "account.v1.pb")
	var storedVal wrapperspb.Int64Value
	_ = proto.Unmarshal(raw, &storedVal)
	if storedVal.Value != 100 || rev != "rev-1" {
		t.Fatalf("storage modified unexpectedly: value=%d, rev=%s", storedVal.Value, rev)
	}
}

func TestDocstore_Update_ContextCancellation(t *testing.T) {
	store := newMockDocStorage()
	initialData, _ := proto.Marshal(wrapperspb.Int64(100))
	store.data["doc.v1.pb"] = initialData
	store.revisions["doc.v1.pb"] = "rev-1"

	tc := &testInt64DocClient{schemaMinor: 1}
	doc := &Document{
		Client:               tc,
		Storage:              store,
		BucketName:           "test-bucket",
		ObjectName:           "doc.v1.pb",
		SupportedSchemaMinor: 1,
		ProtoMsg:             &tc.msg,
	}

	// 1. Pre-canceled context
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	err := doc.Update(canceledCtx, func(msg proto.Message) error {
		if v, ok := msg.(*wrapperspb.Int64Value); ok {
			v.Value = 999
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled error, got: %v", err)
	}

	// Ensure lock was released and not leaked
	lockAcquired := make(chan struct{})
	go func() {
		doc.Rwlock.Lock()
		doc.Rwlock.Unlock()
		close(lockAcquired)
	}()
	select {
	case <-lockAcquired:
		// success, lock is free
	case <-time.After(1 * time.Second):
		t.Fatal("deadlock: doc.Rwlock was not released after context cancellation")
	}

	// 2. Cancellation during updateFn / storage write
	ctx, cancelDuring := context.WithCancel(context.Background())
	err = doc.Update(ctx, func(msg proto.Message) error {
		cancelDuring() // cancel context while in the update cycle
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}

	// Ensure lock was released and subsequent Update with valid context works cleanly
	err = doc.Update(context.Background(), func(msg proto.Message) error {
		if v, ok := msg.(*wrapperspb.Int64Value); ok {
			v.Value = 200
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subsequent update with valid context failed: %v", err)
	}
	if tc.Value() != 200 {
		t.Fatalf("expected value 200, got %d", tc.Value())
	}
}

