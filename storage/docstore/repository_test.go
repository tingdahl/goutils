package docstore

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type testDomainDoc struct {
	Document
	client *testDocClient
}

func newTestDomainDoc(store *mockDocStorage, bucket, object string) (*testDomainDoc, error) {
	tc := &testDocClient{schemaMinor: 0}
	doc := &testDomainDoc{
		client: tc,
	}
	doc.Document = Document{
		Client:               tc,
		Storage:              store,
		BucketName:           bucket,
		ObjectName:           object,
		ProtoMsg:             &tc.msg,
		SupportedSchemaMinor: 1,
	}
	if err := doc.DoLoad(); err != nil {
		return nil, err
	}
	return doc, nil
}

func TestRepository_PresenceHit_AvoidsFactory(t *testing.T) {
	ctx := context.Background()
	store := newMockDocStorage()
	initialData, _ := proto.Marshal(wrapperspb.String("doc1-val"))
	store.data["item-1"] = initialData
	store.revisions["item-1"] = "rev-1"

	var factoryCallCount int32
	factory := func(ctx context.Context, objectPath string) (*testDomainDoc, error) {
		atomic.AddInt32(&factoryCallCount, 1)
		return newTestDomainDoc(store, "test-bucket", objectPath)
	}

	repo := NewRepository(factory, WithRevisionPolicy(CheckNever))

	// First call - should trigger factory
	doc1, err := repo.Get(ctx, "item-1")
	if err != nil {
		t.Fatalf("first Get failed: %v", err)
	}
	if doc1.client.msg.Value != "doc1-val" {
		t.Errorf("expected doc1-val, got %s", doc1.client.msg.Value)
	}
	if atomic.LoadInt32(&factoryCallCount) != 1 {
		t.Fatalf("expected factoryCallCount=1, got %d", factoryCallCount)
	}

	// Second call - presence check should return in-memory instance without calling factory
	doc2, err := repo.Get(ctx, "item-1")
	if err != nil {
		t.Fatalf("second Get failed: %v", err)
	}
	if doc1 != doc2 {
		t.Errorf("expected same pointer instance, got %p vs %p", doc1, doc2)
	}
	if atomic.LoadInt32(&factoryCallCount) != 1 {
		t.Fatalf("factory was called again: %d", factoryCallCount)
	}
}

func TestRepository_GenerationValidation_ReloadsOnNewRevision(t *testing.T) {
	ctx := context.Background()
	store := newMockDocStorage()
	initialData, _ := proto.Marshal(wrapperspb.String("initial"))
	store.data["item-1"] = initialData
	store.revisions["item-1"] = "rev-1"

	factory := func(ctx context.Context, objectPath string) (*testDomainDoc, error) {
		return newTestDomainDoc(store, "test-bucket", objectPath)
	}

	// Always check revision
	repo := NewRepository(factory, WithRevisionPolicy(CheckAlways))

	doc, err := repo.Get(ctx, "item-1")
	if err != nil {
		t.Fatalf("initial Get failed: %v", err)
	}
	if doc.client.msg.Value != "initial" {
		t.Errorf("expected initial, got %s", doc.client.msg.Value)
	}

	// Modify storage behind the scenes with a new revision
	updatedData, _ := proto.Marshal(wrapperspb.String("modified-externally"))
	store.mu.Lock()
	store.data["item-1"] = updatedData
	store.revisions["item-1"] = "rev-2"
	store.mu.Unlock()

	// Get again with CheckAlways -> should detect rev-2 and reload
	docRefreshed, err := repo.Get(ctx, "item-1")
	if err != nil {
		t.Fatalf("second Get failed: %v", err)
	}
	if docRefreshed.client.msg.Value != "modified-externally" {
		t.Errorf("expected modified-externally, got %s", docRefreshed.client.msg.Value)
	}
	if docRefreshed.Revision != "rev-2" {
		t.Errorf("expected revision rev-2, got %s", docRefreshed.Revision)
	}
}

func TestRepository_GenerationValidation_Interval(t *testing.T) {
	ctx := context.Background()
	store := newMockDocStorage()
	initialData, _ := proto.Marshal(wrapperspb.String("v1"))
	store.data["item-1"] = initialData
	store.revisions["item-1"] = "rev-1"

	factory := func(ctx context.Context, objectPath string) (*testDomainDoc, error) {
		return newTestDomainDoc(store, "test-bucket", objectPath)
	}

	// 50ms interval
	repo := NewRepository(factory, WithRevisionPolicy(CheckInterval(50*time.Millisecond)))

	_, err := repo.Get(ctx, "item-1")
	if err != nil {
		t.Fatalf("first Get failed: %v", err)
	}

	// External modification
	store.mu.Lock()
	store.data["item-1"], _ = proto.Marshal(wrapperspb.String("v2"))
	store.revisions["item-1"] = "rev-2"
	store.mu.Unlock()

	// Immediate Get within 50ms - should NOT check storage yet
	docCached, err := repo.Get(ctx, "item-1")
	if err != nil {
		t.Fatalf("cached Get failed: %v", err)
	}
	if docCached.client.msg.Value != "v1" {
		t.Errorf("expected cached v1 before interval elapsed, got %s", docCached.client.msg.Value)
	}

	// Wait for interval to elapse
	time.Sleep(60 * time.Millisecond)

	// Now Get should validate with storage and reload
	docUpdated, err := repo.Get(ctx, "item-1")
	if err != nil {
		t.Fatalf("updated Get failed: %v", err)
	}
	if docUpdated.client.msg.Value != "v2" {
		t.Errorf("expected reloaded v2, got %s", docUpdated.client.msg.Value)
	}
}

func TestRepository_GetOptions_Overrides(t *testing.T) {
	ctx := context.Background()
	store := newMockDocStorage()
	initialData, _ := proto.Marshal(wrapperspb.String("v1"))
	store.data["item-1"] = initialData
	store.revisions["item-1"] = "rev-1"

	factory := func(ctx context.Context, objectPath string) (*testDomainDoc, error) {
		return newTestDomainDoc(store, "test-bucket", objectPath)
	}

	// Policy is CheckNever
	repo := NewRepository(factory, WithRevisionPolicy(CheckNever))

	_, err := repo.Get(ctx, "item-1")
	if err != nil {
		t.Fatalf("first Get failed: %v", err)
	}

	// External update
	store.mu.Lock()
	store.data["item-1"], _ = proto.Marshal(wrapperspb.String("v2"))
	store.revisions["item-1"] = "rev-2"
	store.mu.Unlock()

	// Normal Get returns cached v1 because policy is CheckNever
	docNormal, _ := repo.Get(ctx, "item-1")
	if docNormal.client.msg.Value != "v1" {
		t.Errorf("expected v1 under CheckNever, got %s", docNormal.client.msg.Value)
	}

	// Get with WithForceCheck() forces revision validation
	docForced, err := repo.Get(ctx, "item-1", WithForceCheck())
	if err != nil {
		t.Fatalf("forced Get failed: %v", err)
	}
	if docForced.client.msg.Value != "v2" {
		t.Errorf("expected v2 after WithForceCheck, got %s", docForced.client.msg.Value)
	}
}

func TestRepository_LifetimePruning(t *testing.T) {
	ctx := context.Background()
	store := newMockDocStorage()
	store.data["doc-old"], _ = proto.Marshal(wrapperspb.String("old"))
	store.revisions["doc-old"] = "rev-old"
	store.data["doc-new"], _ = proto.Marshal(wrapperspb.String("new"))
	store.revisions["doc-new"] = "rev-new"

	factory := func(ctx context.Context, objectPath string) (*testDomainDoc, error) {
		return newTestDomainDoc(store, "test-bucket", objectPath)
	}

	// 50ms max lifetime
	repo := NewRepository(factory, WithRevisionPolicy(CheckNever), WithMaxLifetime(50*time.Millisecond))

	// 1. Load doc-old
	_, err := repo.Get(ctx, "doc-old")
	if err != nil {
		t.Fatalf("failed to load doc-old: %v", err)
	}

	// Verify doc-old is in entries
	repo.mu.RLock()
	if len(repo.entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(repo.entries))
	}
	repo.mu.RUnlock()

	// 2. Wait for max lifetime to elapse
	time.Sleep(60 * time.Millisecond)

	// 3. Load doc-new -> Step 3 should prune doc-old because it is older than 50ms
	_, err = repo.Get(ctx, "doc-new")
	if err != nil {
		t.Fatalf("failed to load doc-new: %v", err)
	}

	repo.mu.RLock()
	defer repo.mu.RUnlock()
	if _, ok := repo.entries["doc-old"]; ok {
		t.Errorf("expected doc-old to be pruned by lifetime check, but it still exists")
	}
	if _, ok := repo.entries["doc-new"]; !ok {
		t.Errorf("expected doc-new to be in entries, but not found")
	}
	if len(repo.entries) != 1 {
		t.Errorf("expected exactly 1 entry, got %d", len(repo.entries))
	}
}

func TestRepository_LifetimePruning_PreservesActiveDocument(t *testing.T) {
	ctx := context.Background()
	store := newMockDocStorage()
	store.data["doc-1"], _ = proto.Marshal(wrapperspb.String("active"))
	store.revisions["doc-1"] = "rev-1"

	factory := func(ctx context.Context, objectPath string) (*testDomainDoc, error) {
		return newTestDomainDoc(store, "test-bucket", objectPath)
	}

	repo := NewRepository(factory, WithRevisionPolicy(CheckNever), WithMaxLifetime(50*time.Millisecond))

	// Load doc-1
	doc1, err := repo.Get(ctx, "doc-1")
	if err != nil {
		t.Fatalf("first load failed: %v", err)
	}

	// Wait 60ms so it exceeds max lifetime
	time.Sleep(60 * time.Millisecond)

	// Access doc-1 again: Step 1 presence check should reuse it (regardless of lifetime)
	// and renewal should prevent Step 3 from pruning it
	doc2, err := repo.Get(ctx, "doc-1")
	if err != nil {
		t.Fatalf("second load failed: %v", err)
	}

	if doc1 != doc2 {
		t.Errorf("expected same instance preserved, got %p vs %p", doc1, doc2)
	}

	repo.mu.RLock()
	if len(repo.entries) != 1 {
		t.Errorf("expected doc-1 to still be in repository, entries count: %d", len(repo.entries))
	}
	repo.mu.RUnlock()
}

func TestRepository_SingleflightConcurrency(t *testing.T) {
	ctx := context.Background()
	store := newMockDocStorage()
	store.data["concurrent-doc"], _ = proto.Marshal(wrapperspb.String("data"))
	store.revisions["concurrent-doc"] = "rev-1"

	var factoryCalls int32
	factory := func(ctx context.Context, objectPath string) (*testDomainDoc, error) {
		// Simulate network latency
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&factoryCalls, 1)
		return newTestDomainDoc(store, "test-bucket", objectPath)
	}

	repo := NewRepository(factory, WithRevisionPolicy(CheckNever))

	const numGoroutines = 20
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	results := make([]*testDomainDoc, numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		idx := i
		go func() {
			defer wg.Done()
			doc, err := repo.Get(ctx, "concurrent-doc")
			if err != nil {
				t.Errorf("goroutine %d failed: %v", idx, err)
				return
			}
			results[idx] = doc
		}()
	}

	wg.Wait()

	// Only 1 factory call should have occurred across all 20 concurrent requests
	if calls := atomic.LoadInt32(&factoryCalls); calls != 1 {
		t.Errorf("expected 1 factory call under singleflight, got %d", calls)
	}

	// All returned instances must be the exact same pointer
	first := results[0]
	for i := 1; i < numGoroutines; i++ {
		if results[i] != first {
			t.Errorf("goroutine %d got different instance: %p vs %p", i, results[i], first)
		}
	}
}
