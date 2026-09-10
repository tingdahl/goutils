package billing_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/tingdahl/goutils/billing"
	"github.com/tingdahl/goutils/storage"
)

func TestBillingClient_AddAndGetReceipt(t *testing.T) {
	ctx := context.Background()
	mockStorage := storage.NewMockStorageClient()
	const bucket = "test-billing-bucket"
	const tenantID int64 = 1001

	client, err := billing.NewBillingClient(mockStorage, bucket, tenantID)
	if err != nil {
		t.Fatalf("failed to create billing client: %v", err)
	}

	pdfData := []byte("%PDF-1.4 test receipt invoice data")

	req := &billing.AddReceiptRequestProto{
		Receipt: &billing.PaymentReceiptProto{
			InvoiceId:   "INV-2026-001",
			AmountCents: 19900,
			Currency:    "SEK",
			Description: "Annual Pro Church Subscription",
			ProductId:   "plan_annual_pro",
		},
		PdfContent:  pdfData,
		PdfFilename: "church-inv-001.pdf",
	}

	receipt, err := client.AddReceipt(ctx, req, "user-admin")
	if err != nil {
		t.Fatalf("AddReceipt failed: %v", err)
	}

	if receipt.Id == "" {
		t.Errorf("expected generated receipt ID, got empty string")
	}
	if receipt.AmountCents != 19900 {
		t.Errorf("expected 19900 amount, got %d", receipt.AmountCents)
	}
	if receipt.ReceiptObjectKey == "" {
		t.Errorf("expected non-empty receipt object key")
	}

	// 1. Get receipt by ID
	fetched := client.GetReceipt(receipt.Id)
	if fetched == nil {
		t.Fatalf("GetReceipt by ID returned nil")
	}
	if fetched.InvoiceId != "INV-2026-001" {
		t.Errorf("expected invoice INV-2026-001, got %s", fetched.InvoiceId)
	}

	// 2. Get receipt by InvoiceId
	fetchedByInv := client.GetReceipt("INV-2026-001")
	if fetchedByInv == nil || fetchedByInv.Id != receipt.Id {
		t.Fatalf("GetReceipt by invoiceId failed")
	}

	// 3. List receipts
	list := client.ListReceipts()
	if len(list) != 1 {
		t.Errorf("expected 1 receipt in list, got %d", len(list))
	}

	// 4. Retrieve PDF bytes
	content, meta, err := client.GetReceiptPDF(ctx, receipt.Id)
	if err != nil {
		t.Fatalf("GetReceiptPDF failed: %v", err)
	}
	if !bytes.Equal(content, pdfData) {
		t.Errorf("returned PDF content mismatch")
	}
	if meta.PdfFilename != "church-inv-001.pdf" {
		t.Errorf("expected filename church-inv-001.pdf, got %s", meta.PdfFilename)
	}

	// 5. Retrieve signed download link
	link, _, err := client.GetReceiptDownloadLink(ctx, receipt.Id, 3600)
	if err != nil {
		t.Fatalf("GetReceiptDownloadLink failed: %v", err)
	}
	if link == "" {
		t.Errorf("expected signed download link, got empty")
	}
}

func TestBillingClient_MultipleEntitiesAndReceipts(t *testing.T) {
	ctx := context.Background()
	mockStorage := storage.NewMockStorageClient()
	const bucket = "test-billing-bucket"

	// Tenant 1 adds 2 receipts
	client1, err := billing.NewBillingClient(mockStorage, bucket, 100)
	if err != nil {
		t.Fatalf("failed to create billing client 1: %v", err)
	}

	_, err = client1.AddReceipt(ctx, &billing.AddReceiptRequestProto{
		Receipt: &billing.PaymentReceiptProto{
			InvoiceId:   "INV-100-A",
			AmountCents: 5000,
			Currency:    "USD",
		},
	}, "user-1")
	if err != nil {
		t.Fatalf("AddReceipt failed: %v", err)
	}

	_, err = client1.AddReceipt(ctx, &billing.AddReceiptRequestProto{
		Receipt: &billing.PaymentReceiptProto{
			InvoiceId:   "INV-100-B",
			AmountCents: 7500,
			Currency:    "USD",
		},
	}, "user-1")
	if err != nil {
		t.Fatalf("AddReceipt failed: %v", err)
	}

	// Tenant 2 adds 1 receipt
	client2, err := billing.NewBillingClient(mockStorage, bucket, 200)
	if err != nil {
		t.Fatalf("failed to create billing client 2: %v", err)
	}

	_, err = client2.AddReceipt(ctx, &billing.AddReceiptRequestProto{
		Receipt: &billing.PaymentReceiptProto{
			InvoiceId:   "INV-200-A",
			AmountCents: 12000,
			Currency:    "EUR",
		},
	}, "user-2")
	if err != nil {
		t.Fatalf("AddReceipt failed: %v", err)
	}

	if len(client1.ListReceipts()) != 2 {
		t.Errorf("expected 2 receipts for tenant 100, got %d", len(client1.ListReceipts()))
	}
	if len(client2.ListReceipts()) != 1 {
		t.Errorf("expected 1 receipt for tenant 200, got %d", len(client2.ListReceipts()))
	}
	if client1.GetReceipt("INV-200-A") != nil {
		t.Errorf("tenant 100 should not see tenant 200's receipt")
	}
}

func TestBillingClient_PersistenceReload(t *testing.T) {
	ctx := context.Background()
	mockStorage := storage.NewMockStorageClient()
	const bucket = "test-billing-bucket"
	const tenantID int64 = 555

	client1, err := billing.NewBillingClient(mockStorage, bucket, tenantID)
	if err != nil {
		t.Fatalf("failed to create billing client 1: %v", err)
	}

	r1, err := client1.AddReceipt(ctx, &billing.AddReceiptRequestProto{
		Receipt: &billing.PaymentReceiptProto{
			InvoiceId:   "INV-RELOAD-1",
			AmountCents: 25000,
			Currency:    "SEK",
		},
	}, "admin")
	if err != nil {
		t.Fatalf("AddReceipt failed: %v", err)
	}

	// Reload in a new client instance
	client2, err := billing.NewBillingClient(mockStorage, bucket, tenantID)
	if err != nil {
		t.Fatalf("failed to create billing client 2: %v", err)
	}

	r2 := client2.GetReceipt(r1.Id)
	if r2 == nil {
		t.Fatalf("expected receipt to persist and reload in client 2")
	}
	if r2.InvoiceId != "INV-RELOAD-1" {
		t.Errorf("expected invoice INV-RELOAD-1, got %s", r2.InvoiceId)
	}
	if r2.CreatedAtUnixMs <= 0 {
		t.Errorf("expected valid CreatedAtUnixMs, got %d", r2.CreatedAtUnixMs)
	}
	_ = time.Now()
}

func TestBillingRepository(t *testing.T) {
	ctx := context.Background()
	mockStorage := storage.NewMockStorageClient()
	const bucket = "test-billing-bucket"

	// 1. Validation: uninitialized GetBillingClient fails
	if _, err := billing.GetBillingClient(ctx, 777); err == nil {
		t.Errorf("expected error when GetBillingClient called before Init")
	}

	// 2. Validation: nil store and empty bucket fail
	if err := billing.Init(nil, bucket); err == nil {
		t.Errorf("expected error for nil storage")
	}
	if err := billing.Init(mockStorage, ""); err == nil {
		t.Errorf("expected error for empty bucket")
	}

	// 3. Init with mock storage and bucket
	if err := billing.Init(mockStorage, bucket); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// 4. Validation: invalid tenant ID fails
	if _, err := billing.GetBillingClient(ctx, 0); err == nil {
		t.Errorf("expected error for tenant ID <= 0")
	}

	// 5. In-memory reuse via GetBillingClient for tenant 777
	const tenant1 int64 = 777
	client1, err := billing.GetBillingClient(ctx, tenant1)
	if err != nil {
		t.Fatalf("first GetBillingClient failed: %v", err)
	}
	expectedPath := "/tenant/777/billing.v1.pb.br"
	if client1.ObjectName != expectedPath {
		t.Errorf("expected object path %s, got %s", expectedPath, client1.ObjectName)
	}

	client2, err := billing.GetBillingClient(ctx, tenant1)
	if err != nil {
		t.Fatalf("second GetBillingClient failed: %v", err)
	}
	if client1 != client2 {
		t.Errorf("expected same BillingClient pointer from cache, got %p vs %p", client1, client2)
	}

	// 6. Distinct tenant gets separate client and path
	const tenant2 int64 = 888
	clientOther, err := billing.GetBillingClient(ctx, tenant2)
	if err != nil {
		t.Fatalf("GetBillingClient for other tenant failed: %v", err)
	}
	if client1 == clientOther {
		t.Errorf("expected different BillingClient pointers for different tenants, got same %p", client1)
	}
	expectedOtherPath := "/tenant/888/billing.v1.pb.br"
	if clientOther.ObjectName != expectedOtherPath {
		t.Errorf("expected object path %s, got %s", expectedOtherPath, clientOther.ObjectName)
	}

	// 7. Add receipt through client1 for tenant 777
	r1, err := client1.AddReceipt(ctx, &billing.AddReceiptRequestProto{
		Receipt: &billing.PaymentReceiptProto{
			InvoiceId:   "INV-REPO-1",
			AmountCents: 5000,
			Currency:    "USD",
		},
	}, "admin")
	if err != nil {
		t.Fatalf("AddReceipt failed: %v", err)
	}

	// 8. Subsequent GetBillingClient for tenant 777 sees the added receipt
	client3, err := billing.GetBillingClient(ctx, tenant1)
	if err != nil {
		t.Fatalf("third GetBillingClient failed: %v", err)
	}
	fetchedReceipt := client3.GetReceipt(r1.Id)
	if fetchedReceipt == nil {
		t.Fatalf("expected receipt to be visible in client3")
	}
	if fetchedReceipt.InvoiceId != "INV-REPO-1" {
		t.Errorf("expected invoice INV-REPO-1, got %s", fetchedReceipt.InvoiceId)
	}

	// 9. Verify tenant isolation: tenant 888 does NOT see tenant 777's receipt
	if clientOther.GetReceipt(r1.Id) != nil {
		t.Errorf("tenant 888 should not see tenant 777's receipt")
	}
}
