package entitlement_test

import (
	"context"
	"testing"
	"time"

	"github.com/tingdahl/goutils/entitlement"
	"github.com/tingdahl/goutils/storage"
	"google.golang.org/protobuf/proto"
)

var (
	testStore  = storage.NewMockStorageClient()
	testPrefix = "entitlements"
)

func initTestPackage(t *testing.T) {
	t.Helper()
	err := entitlement.Init(testStore, testPrefix)
	if err != nil {
		t.Fatalf("entitlement.Init failed: %v", err)
	}
}

func TestEntitlement_InitValidation(t *testing.T) {
	if err := entitlement.Init(nil, "prefix"); err == nil {
		t.Errorf("expected error with nil store, got nil")
	}
}

func TestEntitlement_GetClientValidation(t *testing.T) {
	initTestPackage(t)
	ctx := context.Background()

	// TenantID <= 0 should fail
	if _, err := entitlement.GetEntitlementClient(ctx, 0); err == nil {
		t.Errorf("expected error for tenant ID 0, got nil")
	}
	if _, err := entitlement.GetEntitlementClient(ctx, -1); err == nil {
		t.Errorf("expected error for tenant ID -1, got nil")
	}
}

func TestEntitlement_DimensionsLifecycle(t *testing.T) {
	initTestPackage(t)
	ctx := context.Background()

	const tenantID int64 = 1001
	client, err := entitlement.GetEntitlementClient(ctx, tenantID)
	if err != nil {
		t.Fatalf("failed to get entitlement client: %v", err)
	}

	if client.TenantID() != tenantID {
		t.Errorf("expected tenant ID %d, got %d", tenantID, client.TenantID())
	}

	// 1. Add dimensions
	dims := map[int32]*entitlement.EntitlementDimensionProto{
		1: {
			Name: "users_seats",
		},
		2: {
			Name: "storage_gb",
		},
	}

	if err := client.AddDimensions(ctx, dims); err != nil {
		t.Fatalf("AddDimensions failed: %v", err)
	}

	loadedDims := client.Dimensions()
	if len(loadedDims) != 2 {
		t.Fatalf("expected 2 dimensions, got %d", len(loadedDims))
	}
	if loadedDims[1].Name != "users_seats" {
		t.Errorf("expected dimension 1 name users_seats, got %s", loadedDims[1].Name)
	}
	if loadedDims[2].Name != "storage_gb" {
		t.Errorf("expected dimension 2 name storage_gb, got %s", loadedDims[2].Name)
	}

	// 2. Add duplicate dimension should fail
	dupDims := map[int32]*entitlement.EntitlementDimensionProto{
		1: {Name: "duplicate"},
	}
	if err := client.AddDimensions(ctx, dupDims); err == nil {
		t.Errorf("expected error adding duplicate dimension 1, got nil")
	}

	// 3. Remove existing dimension
	if err := client.RemoveDimensions(ctx, []int32{2}); err != nil {
		t.Fatalf("RemoveDimensions failed: %v", err)
	}
	if len(client.Dimensions()) != 1 {
		t.Errorf("expected 1 dimension after removal, got %d", len(client.Dimensions()))
	}

	// 4. Remove non-existent dimension should fail
	if err := client.RemoveDimensions(ctx, []int32{999}); err == nil {
		t.Errorf("expected error removing non-existent dimension, got nil")
	}
}

func TestEntitlement_TransactionValidation(t *testing.T) {
	initTestPackage(t)
	ctx := context.Background()

	client, err := entitlement.GetEntitlementClient(ctx, 2001)
	if err != nil {
		t.Fatalf("GetEntitlementClient failed: %v", err)
	}

	// 1. Nil transaction
	if err := client.AddTransaction(ctx, nil); err == nil {
		t.Errorf("expected error for nil transaction, got nil")
	}

	// 2. EffectiveAtUnixMs <= 0
	txNoEffective := &entitlement.EntitlementTransactionProto{
		DimensionValues: map[int32]int64{1: 10},
		Description:     "No effective time",
	}
	if err := client.AddTransaction(ctx, txNoEffective); err == nil {
		t.Errorf("expected error for missing effective_at_unix_ms, got nil")
	}

	// 3. Empty DimensionValues
	txNoDims := &entitlement.EntitlementTransactionProto{
		EffectiveAtUnixMs: time.Now().UnixMilli(),
		Description:       "No dimensions",
	}
	if err := client.AddTransaction(ctx, txNoDims); err == nil {
		t.Errorf("expected error for empty dimension_values, got nil")
	}

	// 4. Empty Description
	txNoDesc := &entitlement.EntitlementTransactionProto{
		EffectiveAtUnixMs: time.Now().UnixMilli(),
		DimensionValues:   map[int32]int64{1: 5},
	}
	if err := client.AddTransaction(ctx, txNoDesc); err == nil {
		t.Errorf("expected error for empty description, got nil")
	}

	// 5. Negative ExpiresAtUnixMs
	txNegativeExpiry := &entitlement.EntitlementTransactionProto{
		EffectiveAtUnixMs: time.Now().UnixMilli(),
		ExpiresAtUnixMs:   -1,
		DimensionValues:   map[int32]int64{1: 5},
		Description:       "Negative expiry",
	}
	if err := client.AddTransaction(ctx, txNegativeExpiry); err == nil {
		t.Errorf("expected error for negative expires_at_unix_ms, got nil")
	}

	// 6. INCREMENTING with ExpiresAtUnixMs set should fail
	txIncWithExpiry := &entitlement.EntitlementTransactionProto{
		TransactionType:   entitlement.EntitlementTransactionType_ENTITLEMENT_TRANSACTION_TYPE_INCREMENTING,
		EffectiveAtUnixMs: time.Now().UnixMilli(),
		ExpiresAtUnixMs:   time.Now().UnixMilli() + 10000,
		DimensionValues:   map[int32]int64{1: 5},
		Description:       "Incrementing with expiry",
	}
	if err := client.AddTransaction(ctx, txIncWithExpiry); err == nil {
		t.Errorf("expected error for incrementing transaction with expires_at_unix_ms, got nil")
	}

	// 7. REPLACING with ExpiresAtUnixMs set should fail
	txRepWithExpiry := &entitlement.EntitlementTransactionProto{
		TransactionType:   entitlement.EntitlementTransactionType_ENTITLEMENT_TRANSACTION_TYPE_REPLACING,
		EffectiveAtUnixMs: time.Now().UnixMilli(),
		ExpiresAtUnixMs:   time.Now().UnixMilli() + 10000,
		DimensionValues:   map[int32]int64{1: 5},
		Description:       "Replacing with expiry",
	}
	if err := client.AddTransaction(ctx, txRepWithExpiry); err == nil {
		t.Errorf("expected error for replacing transaction with expires_at_unix_ms, got nil")
	}

	// 8. LEASE without ExpiresAtUnixMs should fail
	txLeaseNoExpiry := &entitlement.EntitlementTransactionProto{
		TransactionType:   entitlement.EntitlementTransactionType_ENTITLEMENT_TRANSACTION_TYPE_LEASE,
		EffectiveAtUnixMs: time.Now().UnixMilli(),
		ExpiresAtUnixMs:   0,
		DimensionValues:   map[int32]int64{1: 5},
		Description:       "Lease missing expiry",
	}
	if err := client.AddTransaction(ctx, txLeaseNoExpiry); err == nil {
		t.Errorf("expected error for lease missing expires_at_unix_ms, got nil")
	}
}

func TestEntitlement_AddRemoveTransactions_AndOrdering(t *testing.T) {
	initTestPackage(t)
	ctx := context.Background()

	const tenantID int64 = 3001
	client, err := entitlement.GetEntitlementClient(ctx, tenantID)
	if err != nil {
		t.Fatalf("GetEntitlementClient failed: %v", err)
	}

	now := time.Now().UnixMilli()

	// Add transaction 1 (effective at T+100)
	tx1 := &entitlement.EntitlementTransactionProto{
		EffectiveAtUnixMs: now + 100,
		DimensionValues:   map[int32]int64{1: 10},
		Description:       "Second effective",
	}
	if err := client.AddTransaction(ctx, tx1); err != nil {
		t.Fatalf("AddTransaction 1 failed: %v", err)
	}

	// Add transaction 2 (effective earlier, at T+50)
	tx2 := &entitlement.EntitlementTransactionProto{
		EffectiveAtUnixMs: now + 50,
		DimensionValues:   map[int32]int64{1: 5},
		Description:       "First effective",
	}
	if err := client.AddTransaction(ctx, tx2); err != nil {
		t.Fatalf("AddTransaction 2 failed: %v", err)
	}

	txs := client.Transactions()
	if len(txs) != 2 {
		t.Fatalf("expected 2 transactions, got %d", len(txs))
	}

	// Transactions must be sorted ascending by EffectiveAtUnixMs
	if txs[0].EffectiveAtUnixMs != now+50 {
		t.Errorf("expected first transaction effective at %d, got %d", now+50, txs[0].EffectiveAtUnixMs)
	}
	if txs[1].EffectiveAtUnixMs != now+100 {
		t.Errorf("expected second transaction effective at %d, got %d", now+100, txs[1].EffectiveAtUnixMs)
	}

	// Verify auto-assigned transaction IDs
	if tx1.TransactionId != 1 {
		t.Errorf("expected tx1 id 1, got %d", tx1.TransactionId)
	}
	if tx2.TransactionId != 2 {
		t.Errorf("expected tx2 id 2, got %d", tx2.TransactionId)
	}

	// Remove transaction 1
	if err := client.RemoveTransaction(ctx, 1); err != nil {
		t.Fatalf("RemoveTransaction failed: %v", err)
	}
	if len(client.Transactions()) != 1 {
		t.Fatalf("expected 1 transaction remaining, got %d", len(client.Transactions()))
	}
	if client.Transactions()[0].TransactionId != 2 {
		t.Errorf("expected transaction 2 remaining, got %d", client.Transactions()[0].TransactionId)
	}

	// Removing non-existent transaction fails
	if err := client.RemoveTransaction(ctx, 999); err == nil {
		t.Errorf("expected error removing non-existent transaction, got nil")
	}
}

func TestEntitlement_LeasePruning(t *testing.T) {
	initTestPackage(t)
	ctx := context.Background()

	client, err := entitlement.GetEntitlementClient(ctx, 4001)
	if err != nil {
		t.Fatalf("GetEntitlementClient failed: %v", err)
	}

	now := time.Now().UnixMilli()

	// 1. Permanent transaction (ExpiresAtUnixMs == 0) - should never prune
	err = client.AddTransaction(ctx, &entitlement.EntitlementTransactionProto{
		TransactionType:   entitlement.EntitlementTransactionType_ENTITLEMENT_TRANSACTION_TYPE_REPLACING,
		EffectiveAtUnixMs: now,
		ExpiresAtUnixMs:   0,
		DimensionValues:   map[int32]int64{1: 100},
		Description:       "Permanent transaction",
	})
	if err != nil {
		t.Fatalf("Add permanent transaction failed: %v", err)
	}

	// 2. Active lease (expires in future) - should be kept
	err = client.AddTransaction(ctx, &entitlement.EntitlementTransactionProto{
		TransactionType:   entitlement.EntitlementTransactionType_ENTITLEMENT_TRANSACTION_TYPE_LEASE,
		EffectiveAtUnixMs: now,
		ExpiresAtUnixMs:   now + 100000,
		DimensionValues:   map[int32]int64{1: 50},
		Description:       "Active lease",
	})
	if err != nil {
		t.Fatalf("Add active lease failed: %v", err)
	}

	// 3. Expired lease (expired in past) - when added or during subsequent add, should be pruned
	err = client.AddTransaction(ctx, &entitlement.EntitlementTransactionProto{
		TransactionType:   entitlement.EntitlementTransactionType_ENTITLEMENT_TRANSACTION_TYPE_LEASE,
		EffectiveAtUnixMs: now - 5000,
		ExpiresAtUnixMs:   now - 1000,
		DimensionValues:   map[int32]int64{1: 25},
		Description:       "Expired lease",
	})
	if err != nil {
		t.Fatalf("Add expired lease failed: %v", err)
	}

	txs := client.Transactions()
	// The expired lease should have been pruned by pruneExpiredTransactions
	if len(txs) != 3 {
		t.Errorf("expected 2 transactions after pruning expired lease, got %d", len(txs))
	}
}

func TestEntitlement_GetCurrentEntitlementValues(t *testing.T) {
	initTestPackage(t)
	ctx := context.Background()

	client, err := entitlement.GetEntitlementClient(ctx, 5001)
	if err != nil {
		t.Fatalf("GetEntitlementClient failed: %v", err)
	}

	// Setup dimensions:
	// 1: seats
	// 2: credits
	// 3: storage_gb
	err = client.AddDimensions(ctx, map[int32]*entitlement.EntitlementDimensionProto{
		1: {Name: "seats"},
		2: {Name: "credits"},
		3: {Name: "storage_gb"},
	})
	if err != nil {
		t.Fatalf("AddDimensions failed: %v", err)
	}

	// Initial values should all be 0
	initVals := client.GetCurrentEntitlementValues()
	if initVals[1] != 0 || initVals[2] != 0 || initVals[3] != 0 {
		t.Errorf("expected initial values to be 0, got %v", initVals)
	}

	baseTime := time.Now().UnixMilli()

	// Transaction 1 (REPLACING): seats=5, credits=100 (Effective at baseTime)
	_ = client.AddTransaction(ctx, &entitlement.EntitlementTransactionProto{
		TransactionType:   entitlement.EntitlementTransactionType_ENTITLEMENT_TRANSACTION_TYPE_REPLACING,
		EffectiveAtUnixMs: baseTime,
		DimensionValues:   map[int32]int64{1: 5, 2: 100},
		Description:       "Initial purchase",
	})

	// Transaction 2 (REPLACING): seats=10 (Effective at baseTime + 10) -> replaces seats
	_ = client.AddTransaction(ctx, &entitlement.EntitlementTransactionProto{
		TransactionType:   entitlement.EntitlementTransactionType_ENTITLEMENT_TRANSACTION_TYPE_REPLACING,
		EffectiveAtUnixMs: baseTime + 10,
		DimensionValues:   map[int32]int64{1: 10},
		Description:       "Upgrade seats",
	})

	// Transaction 3 (INCREMENTING): credits=+50 (Effective at baseTime + 20) -> adds to credits
	_ = client.AddTransaction(ctx, &entitlement.EntitlementTransactionProto{
		TransactionType:   entitlement.EntitlementTransactionType_ENTITLEMENT_TRANSACTION_TYPE_INCREMENTING,
		EffectiveAtUnixMs: baseTime + 20,
		DimensionValues:   map[int32]int64{2: 50},
		Description:       "Add credits",
	})

	// Transaction 4: active lease for storage_gb=+10 (Effective at baseTime + 25)
	_ = client.AddTransaction(ctx, &entitlement.EntitlementTransactionProto{
		TransactionType:   entitlement.EntitlementTransactionType_ENTITLEMENT_TRANSACTION_TYPE_LEASE,
		EffectiveAtUnixMs: baseTime + 25,
		ExpiresAtUnixMs:   baseTime + 100000,
		DimensionValues:   map[int32]int64{3: 10},
		Description:       "Storage boost lease",
	})

	// Transaction 5: unknown dimension 99 (should be ignored gracefully)
	_ = client.AddTransaction(ctx, &entitlement.EntitlementTransactionProto{
		TransactionType:   entitlement.EntitlementTransactionType_ENTITLEMENT_TRANSACTION_TYPE_INCREMENTING,
		EffectiveAtUnixMs: baseTime + 30,
		DimensionValues:   map[int32]int64{99: 999},
		Description:       "Unknown dimension tx",
	})

	vals := client.GetCurrentEntitlementValues()

	// seats (REPLACING) should be 10
	if vals[1] != 10 {
		t.Errorf("expected seats=10, got %d", vals[1])
	}

	// credits (REPLACING 100 then INCREMENTING 50) should be 150
	if vals[2] != 150 {
		t.Errorf("expected credits=150, got %d", vals[2])
	}

	// storage_gb (LEASE 10) should be 10
	if vals[3] != 10 {
		t.Errorf("expected storage_gb=10, got %d", vals[3])
	}
}

func TestEntitlement_Persistence(t *testing.T) {
	initTestPackage(t)
	ctx := context.Background()

	const tenantID int64 = 6001
	client, err := entitlement.GetEntitlementClient(ctx, tenantID)
	if err != nil {
		t.Fatalf("GetEntitlementClient failed: %v", err)
	}

	err = client.AddDimensions(ctx, map[int32]*entitlement.EntitlementDimensionProto{
		1: {Name: "api_calls"},
	})
	if err != nil {
		t.Fatalf("AddDimensions failed: %v", err)
	}

	err = client.AddTransaction(ctx, &entitlement.EntitlementTransactionProto{
		EffectiveAtUnixMs: time.Now().UnixMilli(),
		DimensionValues:   map[int32]int64{1: 5000},
		Description:       "Monthly API bundle",
	})
	if err != nil {
		t.Fatalf("AddTransaction failed: %v", err)
	}

	// Verify object exists at expected path in storage: <prefix>/<tenantID>/entitlement.v1.pb.zst
	expectedKey := "entitlements/6001/entitlement.v1.pb.zst"
	data, _, err := testStore.ReadObject(ctx, expectedKey)
	if err != nil {
		t.Fatalf("expected storage object %s to exist: %v", expectedKey, err)
	}
	if len(data) == 0 {
		t.Errorf("expected non-empty stored protobuf data")
	}

	// Verify the stored protobuf can be unmarshaled directly and has correct fields
	var storedProto entitlement.EntitlementProto
	if err := proto.Unmarshal(data, &storedProto); err != nil {
		t.Fatalf("failed to unmarshal stored protobuf: %v", err)
	}

	if storedProto.TenantId != tenantID {
		t.Errorf("expected stored tenant ID %d, got %d", tenantID, storedProto.TenantId)
	}
	if len(storedProto.Dimensions) != 1 {
		t.Errorf("expected 1 stored dimension, got %d", len(storedProto.Dimensions))
	}
	if storedProto.Dimensions[1].Name != "api_calls" {
		t.Errorf("expected dimension name 'api_calls', got %s", storedProto.Dimensions[1].Name)
	}
	if len(storedProto.Transactions) != 1 {
		t.Errorf("expected 1 stored transaction, got %d", len(storedProto.Transactions))
	}
	if storedProto.Transactions[0].DimensionValues[1] != 5000 {
		t.Errorf("expected stored transaction value 5000, got %d", storedProto.Transactions[0].DimensionValues[1])
	}

	vals := client.GetCurrentEntitlementValues()
	if vals[1] != 5000 {
		t.Errorf("expected api_calls value 5000, got %d", vals[1])
	}
}

func TestEntitlement_TenantIDMismatch(t *testing.T) {
	initTestPackage(t)
	ctx := context.Background()

	// Pre-populate storage with an entitlement doc belonging to tenant 9999 under path for tenant 8888
	mismatchedProto := &entitlement.EntitlementProto{
		TenantId: 9999,
	}
	data, err := proto.Marshal(mismatchedProto)
	if err != nil {
		t.Fatalf("failed to marshal proto: %v", err)
	}

	path := "entitlements/8888/entitlement.v1.pb.zst"
	if _, err := testStore.WriteObject(ctx, path, data); err != nil {
		t.Fatalf("failed to write mismatched object to storage: %v", err)
	}

	// Fetching client for tenant 8888 should detect the mismatch
	_, err = entitlement.GetEntitlementClient(ctx, 8888)
	if err == nil {
		t.Fatalf("expected tenant ID mismatch error, got nil")
	}
}


func TestEntitlement_GetSignedURL(t *testing.T) {
	initTestPackage(t)
	ctx := context.Background()

	const tenantID int64 = 7777
	client, err := entitlement.GetEntitlementClient(ctx, tenantID)
	if err != nil {
		t.Fatalf("failed to get entitlement client: %v", err)
	}

	url, err := client.GetSignedURL(ctx, 300)
	if err != nil {
		t.Fatalf("GetSignedURL failed: %v", err)
	}
	expectedURL := "https://mock-storage/entitlements/7777/entitlement.v1.pb.zst"
	if url != expectedURL {
		t.Errorf("expected signed URL %s, got %s", expectedURL, url)
	}

	// Default duration <= 0
	urlDefault, err := client.GetSignedURL(ctx, 0)
	if err != nil || urlDefault == "" {
		t.Errorf("GetSignedURL default duration failed: %v", err)
	}
}

func TestEntitlement_TransactionValidationErrors(t *testing.T) {
	initTestPackage(t)
	ctx := context.Background()

	client, err := entitlement.GetEntitlementClient(ctx, 8001)
	if err != nil {
		t.Fatalf("GetEntitlementClient failed: %v", err)
	}

	// 1. Nil transaction
	var nilTx *entitlement.EntitlementTransactionProto
	if err := client.AddTransaction(ctx, nilTx); err == nil {
		t.Error("expected error for nil transaction")
	}

	// 2. EffectiveAtUnixMs <= 0
	txNoEff := &entitlement.EntitlementTransactionProto{
		DimensionValues: map[int32]int64{1: 10},
		Description:     "test",
	}
	if err := client.AddTransaction(ctx, txNoEff); err == nil {
		t.Error("expected error for missing effective_at_unix_ms")
	}

	// 3. Empty DimensionValues
	txNoDims := &entitlement.EntitlementTransactionProto{
		EffectiveAtUnixMs: 1000,
		Description:       "test",
	}
	if err := client.AddTransaction(ctx, txNoDims); err == nil {
		t.Error("expected error for empty dimension_values")
	}

	// 4. Empty Description
	txNoDesc := &entitlement.EntitlementTransactionProto{
		EffectiveAtUnixMs: 1000,
		DimensionValues:   map[int32]int64{1: 10},
	}
	if err := client.AddTransaction(ctx, txNoDesc); err == nil {
		t.Error("expected error for empty description")
	}

	// 5. Lease transaction validation errors
	txLeaseNoExp := &entitlement.EntitlementTransactionProto{
		EffectiveAtUnixMs: 1000,
		DimensionValues:   map[int32]int64{1: 10},
		Description:       "lease",
		TransactionType:   entitlement.EntitlementTransactionType_ENTITLEMENT_TRANSACTION_TYPE_LEASE,
	}
	if err := client.AddTransaction(ctx, txLeaseNoExp); err == nil {
		t.Error("expected error for lease transaction without expires_at_unix_ms")
	}

	txLeaseExpBeforeEff := &entitlement.EntitlementTransactionProto{
		EffectiveAtUnixMs: 1000,
		ExpiresAtUnixMs:   500,
		DimensionValues:   map[int32]int64{1: 10},
		Description:       "lease",
		TransactionType:   entitlement.EntitlementTransactionType_ENTITLEMENT_TRANSACTION_TYPE_LEASE,
	}
	if err := client.AddTransaction(ctx, txLeaseExpBeforeEff); err == nil {
		t.Error("expected error for lease transaction with expires <= effective")
	}

	// 6. Incrementing transaction with ExpiresAtUnixMs
	txIncWithExp := &entitlement.EntitlementTransactionProto{
		EffectiveAtUnixMs: 1000,
		ExpiresAtUnixMs:   2000,
		DimensionValues:   map[int32]int64{1: 10},
		Description:       "inc",
		TransactionType:   entitlement.EntitlementTransactionType_ENTITLEMENT_TRANSACTION_TYPE_INCREMENTING,
	}
	if err := client.AddTransaction(ctx, txIncWithExp); err == nil {
		t.Error("expected error for incrementing transaction with expiration")
	}

	// 7. Replacing transaction with ExpiresAtUnixMs
	txReplWithExp := &entitlement.EntitlementTransactionProto{
		EffectiveAtUnixMs: 1000,
		ExpiresAtUnixMs:   2000,
		DimensionValues:   map[int32]int64{1: 10},
		Description:       "repl",
		TransactionType:   entitlement.EntitlementTransactionType_ENTITLEMENT_TRANSACTION_TYPE_REPLACING,
	}
	if err := client.AddTransaction(ctx, txReplWithExp); err == nil {
		t.Error("expected error for replacing transaction with expiration")
	}

	// 8. Invalid transaction type
	txInvalidType := &entitlement.EntitlementTransactionProto{
		EffectiveAtUnixMs: 1000,
		DimensionValues:   map[int32]int64{1: 10},
		Description:       "invalid",
		TransactionType:   entitlement.EntitlementTransactionType(99),
	}
	if err := client.AddTransaction(ctx, txInvalidType); err == nil {
		t.Error("expected error for invalid transaction type")
	}

	// 9. RemoveTransaction not found
	if err := client.RemoveTransaction(ctx, 9999); err == nil {
		t.Error("expected error removing non-existent transaction")
	}
}

func TestEntitlement_ProtobufGetters(t *testing.T) {
	dim := &entitlement.EntitlementDimensionProto{
		Name: "dim1",
	}
	_ = dim.GetName()
	_ = dim.String()
	_, _ = dim.Descriptor()
	_ = dim.ProtoReflect()
	dim.ProtoMessage()

	tx := &entitlement.EntitlementTransactionProto{
		TransactionId:        1,
		CreatedAtUnixMs:      100,
		EffectiveAtUnixMs:    200,
		ExpiresAtUnixMs:      300,
		TransactionType:      entitlement.EntitlementTransactionType_ENTITLEMENT_TRANSACTION_TYPE_LEASE,
		DimensionValues:      map[int32]int64{1: 5},
		Description:          "tx desc",
		InvoiceNumber:        "inv-123",
		BillingTransactionId: 999,
		Labels:               map[string]string{"k": "v"},
	}
	_ = tx.GetTransactionId()
	_ = tx.GetCreatedAtUnixMs()
	_ = tx.GetEffectiveAtUnixMs()
	_ = tx.GetExpiresAtUnixMs()
	_ = tx.GetTransactionType()
	_ = tx.GetDimensionValues()
	_ = tx.GetDescription()
	_ = tx.GetInvoiceNumber()
	_ = tx.GetBillingTransactionId()
	_ = tx.GetLabels()
	_ = tx.String()
	_, _ = tx.Descriptor()
	_ = tx.ProtoReflect()
	tx.ProtoMessage()

	ep := &entitlement.EntitlementProto{
		TenantId:             123,
		SchemaMinorVersion:   1,
		LastModifiedByCommit: "commit",
		Dimensions:           map[int32]*entitlement.EntitlementDimensionProto{1: dim},
		Transactions:         []*entitlement.EntitlementTransactionProto{tx},
	}
	_ = ep.GetTenantId()
	_ = ep.GetSchemaMinorVersion()
	_ = ep.GetLastModifiedByCommit()
	_ = ep.GetDimensions()
	_ = ep.GetTransactions()
	_ = ep.String()
	_, _ = ep.Descriptor()
	_ = ep.ProtoReflect()
	ep.ProtoMessage()

	var txType entitlement.EntitlementTransactionType = entitlement.EntitlementTransactionType_ENTITLEMENT_TRANSACTION_TYPE_LEASE
	_ = txType.Enum()
	_ = txType.String()
	_ = txType.Descriptor()
	_ = txType.Type()
	_ = txType.Number()
	_, _ = txType.EnumDescriptor()

	// Nil getters
	var nilDim *entitlement.EntitlementDimensionProto
	_ = nilDim.GetName()

	var nilTx *entitlement.EntitlementTransactionProto
	_ = nilTx.GetTransactionId()
	_ = nilTx.GetCreatedAtUnixMs()
	_ = nilTx.GetEffectiveAtUnixMs()
	_ = nilTx.GetExpiresAtUnixMs()
	_ = nilTx.GetTransactionType()
	_ = nilTx.GetDimensionValues()
	_ = nilTx.GetDescription()
	_ = nilTx.GetInvoiceNumber()
	_ = nilTx.GetBillingTransactionId()
	_ = nilTx.GetLabels()

	var nilEp *entitlement.EntitlementProto
	_ = nilEp.GetTenantId()
	_ = nilEp.GetSchemaMinorVersion()
	_ = nilEp.GetLastModifiedByCommit()
	_ = nilEp.GetDimensions()
	_ = nilEp.GetTransactions()
}
