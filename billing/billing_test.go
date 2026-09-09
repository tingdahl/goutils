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

	client, err := billing.NewBillingClient(mockStorage, bucket)
	if err != nil {
		t.Fatalf("failed to create billing client: %v", err)
	}

	const entityID int64 = 1001
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

	receipt, err := client.AddReceipt(ctx, entityID, req, "user-admin")
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
	fetched := client.GetReceipt(entityID, receipt.Id)
	if fetched == nil {
		t.Fatalf("GetReceipt by ID returned nil")
	}
	if fetched.InvoiceId != "INV-2026-001" {
		t.Errorf("expected invoice INV-2026-001, got %s", fetched.InvoiceId)
	}

	// 2. Get receipt by InvoiceId
	fetchedByInv := client.GetReceipt(entityID, "INV-2026-001")
	if fetchedByInv == nil || fetchedByInv.Id != receipt.Id {
		t.Fatalf("GetReceipt by invoiceId failed")
	}

	// 3. List receipts
	list := client.ListReceipts(entityID)
	if len(list) != 1 {
		t.Errorf("expected 1 receipt in list, got %d", len(list))
	}

	// 4. Retrieve PDF bytes
	content, meta, err := client.GetReceiptPDF(ctx, entityID, receipt.Id)
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
	link, _, err := client.GetReceiptDownloadLink(ctx, entityID, receipt.Id, 3600)
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

	client, err := billing.NewBillingClient(mockStorage, bucket)
	if err != nil {
		t.Fatalf("failed to create billing client: %v", err)
	}

	// Entity 1 adds 2 receipts
	_, err = client.AddReceipt(ctx, 100, &billing.AddReceiptRequestProto{
		Receipt: &billing.PaymentReceiptProto{
			InvoiceId:   "INV-100-A",
			AmountCents: 5000,
			Currency:    "USD",
		},
	}, "user-1")
	if err != nil {
		t.Fatalf("AddReceipt failed: %v", err)
	}

	_, err = client.AddReceipt(ctx, 100, &billing.AddReceiptRequestProto{
		Receipt: &billing.PaymentReceiptProto{
			InvoiceId:   "INV-100-B",
			AmountCents: 7500,
			Currency:    "USD",
		},
	}, "user-1")
	if err != nil {
		t.Fatalf("AddReceipt failed: %v", err)
	}

	// Entity 2 adds 1 receipt
	_, err = client.AddReceipt(ctx, 200, &billing.AddReceiptRequestProto{
		Receipt: &billing.PaymentReceiptProto{
			InvoiceId:   "INV-200-A",
			AmountCents: 12000,
			Currency:    "EUR",
		},
	}, "user-2")
	if err != nil {
		t.Fatalf("AddReceipt failed: %v", err)
	}

	if len(client.ListReceipts(100)) != 2 {
		t.Errorf("expected 2 receipts for entity 100, got %d", len(client.ListReceipts(100)))
	}
	if len(client.ListReceipts(200)) != 1 {
		t.Errorf("expected 1 receipt for entity 200, got %d", len(client.ListReceipts(200)))
	}
	if len(client.ListReceipts(999)) != 0 {
		t.Errorf("expected 0 receipts for unknown entity, got %d", len(client.ListReceipts(999)))
	}

	all := client.GetAllEntityBilling()
	if len(all) != 2 {
		t.Errorf("expected 2 entities in GetAllEntityBilling, got %d", len(all))
	}
}

func TestBillingClient_PersistenceReload(t *testing.T) {
	ctx := context.Background()
	mockStorage := storage.NewMockStorageClient()
	const bucket = "test-billing-bucket"

	client1, err := billing.NewBillingClient(mockStorage, bucket)
	if err != nil {
		t.Fatalf("failed to create billing client 1: %v", err)
	}

	const entityID int64 = 555
	r1, err := client1.AddReceipt(ctx, entityID, &billing.AddReceiptRequestProto{
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
	client2, err := billing.NewBillingClient(mockStorage, bucket)
	if err != nil {
		t.Fatalf("failed to create billing client 2: %v", err)
	}

	r2 := client2.GetReceipt(entityID, r1.Id)
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
