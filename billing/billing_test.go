package billing_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/tingdahl/goutils/billing"
	"github.com/tingdahl/goutils/storage"
	"google.golang.org/protobuf/proto"
)

var (
	testStore  = storage.NewMockStorageClient()
	testBucket = "test-billing-bucket"
	testPrefix = "tenant"
)

func initTestPackage(t *testing.T) {
	t.Helper()
	err := billing.Init(testStore, testPrefix)
	if err != nil {
		t.Fatalf("billing.Init failed: %v", err)
	}
}

func TestBilling_InitValidation(t *testing.T) {
	if err := billing.Init(nil, "prefix"); err == nil {
		t.Errorf("expected error with nil store, got nil")
	}
}

func TestBilling_GetClientValidation(t *testing.T) {
	initTestPackage(t)
	ctx := context.Background()

	// TenantID <= 0 should fail
	if _, err := billing.GetBillingClient(ctx, 0); err == nil {
		t.Errorf("expected error for tenant ID 0, got nil")
	}
	if _, err := billing.GetBillingClient(ctx, -1); err == nil {
		t.Errorf("expected error for tenant ID -1, got nil")
	}
}

func TestBillingClient_AddAndGetReceipt(t *testing.T) {
	initTestPackage(t)
	ctx := context.Background()

	const tenantID int64 = 1001
	client, err := billing.GetBillingClient(ctx, tenantID)
	if err != nil {
		t.Fatalf("GetBillingClient failed: %v", err)
	}

	if client.TenantID() != tenantID {
		t.Errorf("expected tenant ID %d, got %d", tenantID, client.TenantID())
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
		t.Errorf("expected non-empty generated receipt ID")
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
		t.Fatalf("GetReceipt by invoice ID failed")
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

func TestBillingClient_AttachReceiptPDF(t *testing.T) {
	initTestPackage(t)
	ctx := context.Background()

	const tenantID int64 = 1005
	client, err := billing.GetBillingClient(ctx, tenantID)
	if err != nil {
		t.Fatalf("GetBillingClient failed: %v", err)
	}

	// 1. Add receipt with NO PDF
	req := &billing.AddReceiptRequestProto{
		Receipt: &billing.PaymentReceiptProto{
			InvoiceId:   "INV-NO-PDF-1",
			AmountCents: 5000,
			Currency:    "USD",
			Description: "Initial credit without pdf",
		},
	}
	r, err := client.AddReceipt(ctx, req, "user-test")
	if err != nil {
		t.Fatalf("AddReceipt failed: %v", err)
	}
	if r.ReceiptObjectKey != "" {
		t.Errorf("expected empty ReceiptObjectKey, got %s", r.ReceiptObjectKey)
	}

	// 2. Attach PDF asynchronously
	pdfBytes := []byte("%PDF-1.4 async receipt data")
	updated, err := client.AttachReceiptPDF(ctx, r.Id, pdfBytes, "invoice-no-pdf-1.pdf")
	if err != nil {
		t.Fatalf("AttachReceiptPDF failed: %v", err)
	}
	if updated.ReceiptObjectKey == "" {
		t.Errorf("expected populated ReceiptObjectKey")
	}
	if updated.SizeBytes != int64(len(pdfBytes)) {
		t.Errorf("expected size %d, got %d", len(pdfBytes), updated.SizeBytes)
	}

	// 3. Verify GetReceipt and GetReceiptPDF work with attached PDF
	fetched := client.GetReceipt(r.Id)
	if fetched.ReceiptObjectKey != updated.ReceiptObjectKey {
		t.Errorf("persisted receipt missing updated object key")
	}

	content, meta, err := client.GetReceiptPDF(ctx, r.Id)
	if err != nil {
		t.Fatalf("GetReceiptPDF failed: %v", err)
	}
	if !bytes.Equal(content, pdfBytes) {
		t.Errorf("pdf content mismatch")
	}
	if meta.PdfFilename != "invoice-no-pdf-1.pdf" {
		t.Errorf("filename mismatch: %s", meta.PdfFilename)
	}
}

func TestBillingClient_MultipleTenantsAndIsolation(t *testing.T) {
	initTestPackage(t)
	ctx := context.Background()

	client1, err := billing.GetBillingClient(ctx, 2001)
	if err != nil {
		t.Fatalf("GetBillingClient 2001 failed: %v", err)
	}

	client2, err := billing.GetBillingClient(ctx, 2002)
	if err != nil {
		t.Fatalf("GetBillingClient 2002 failed: %v", err)
	}

	// Client 1 adds 2 receipts
	_, err = client1.AddReceipt(ctx, &billing.AddReceiptRequestProto{
		Receipt: &billing.PaymentReceiptProto{
			InvoiceId:   "INV-2001-A",
			AmountCents: 5000,
			Currency:    "USD",
		},
	}, "user-1")
	if err != nil {
		t.Fatalf("AddReceipt client1 A failed: %v", err)
	}

	_, err = client1.AddReceipt(ctx, &billing.AddReceiptRequestProto{
		Receipt: &billing.PaymentReceiptProto{
			InvoiceId:   "INV-2001-B",
			AmountCents: 7500,
			Currency:    "USD",
		},
	}, "user-1")
	if err != nil {
		t.Fatalf("AddReceipt client1 B failed: %v", err)
	}

	// Client 2 adds 1 receipt
	r2, err := client2.AddReceipt(ctx, &billing.AddReceiptRequestProto{
		Receipt: &billing.PaymentReceiptProto{
			InvoiceId:   "INV-2002-A",
			AmountCents: 12000,
			Currency:    "EUR",
		},
	}, "user-2")
	if err != nil {
		t.Fatalf("AddReceipt client2 failed: %v", err)
	}

	if len(client1.ListReceipts()) != 2 {
		t.Errorf("expected 2 receipts for tenant 2001, got %d", len(client1.ListReceipts()))
	}
	if len(client2.ListReceipts()) != 1 {
		t.Errorf("expected 1 receipt for tenant 2002, got %d", len(client2.ListReceipts()))
	}

	// Isolation: client 1 cannot access client 2's receipt
	if client1.GetReceipt(r2.Id) != nil {
		t.Errorf("tenant 2001 should not see tenant 2002's receipt")
	}
}

func TestBillingClient_Persistence(t *testing.T) {
	initTestPackage(t)
	ctx := context.Background()

	const tenantID int64 = 3001
	client, err := billing.GetBillingClient(ctx, tenantID)
	if err != nil {
		t.Fatalf("GetBillingClient failed: %v", err)
	}

	r, err := client.AddReceipt(ctx, &billing.AddReceiptRequestProto{
		Receipt: &billing.PaymentReceiptProto{
			InvoiceId:   "INV-PERSIST-1",
			AmountCents: 25000,
			Currency:    "SEK",
		},
	}, "admin")
	if err != nil {
		t.Fatalf("AddReceipt failed: %v", err)
	}

	// Read raw storage object directly to check persistence and tenant ID stamping
	storagePath := billing.TenantBillingPath(tenantID)
	data, _, err := testStore.ReadObject(ctx, storagePath)
	if err != nil {
		t.Fatalf("expected storage object %s to exist: %v", storagePath, err)
	}

	var stored billing.BillingProto
	if err := proto.Unmarshal(data, &stored); err != nil {
		t.Fatalf("failed to unmarshal stored proto: %v", err)
	}

	if stored.TenantId != tenantID {
		t.Errorf("expected stored tenant ID %d, got %d", tenantID, stored.TenantId)
	}
	if len(stored.Receipts) != 1 {
		t.Fatalf("expected 1 stored receipt, got %d", len(stored.Receipts))
	}
	if stored.Receipts[0].Id != r.Id {
		t.Errorf("expected receipt ID %s, got %s", r.Id, stored.Receipts[0].Id)
	}
}

func TestBillingClient_TenantIDMismatch(t *testing.T) {
	initTestPackage(t)
	ctx := context.Background()

	// Pre-populate storage with a billing doc belonging to tenant 9999 under path for tenant 8888
	mismatchedProto := &billing.BillingProto{
		TenantId: 9999,
	}
	data, err := proto.Marshal(mismatchedProto)
	if err != nil {
		t.Fatalf("failed to marshal proto: %v", err)
	}

	path := billing.TenantBillingPath(8888)
	if _, err := testStore.WriteObject(ctx, path, data); err != nil {
		t.Fatalf("failed to write mismatched object to storage: %v", err)
	}

	// Fetching client for tenant 8888 should detect the mismatch
	_, err = billing.GetBillingClient(ctx, 8888)
	if err == nil {
		t.Fatalf("expected tenant ID mismatch error, got nil")
	}
}

func TestBillingClient_ErrorsAndEdgeCases(t *testing.T) {
	initTestPackage(t)
	ctx := context.Background()

	client, err := billing.GetBillingClient(ctx, 4001)
	if err != nil {
		t.Fatalf("GetBillingClient failed: %v", err)
	}

	// 1. AddReceipt nil checks
	if _, err := client.AddReceipt(ctx, nil, "admin"); err != billing.ErrNilReceipt {
		t.Errorf("expected ErrNilReceipt for nil req, got %v", err)
	}
	if _, err := client.AddReceipt(ctx, &billing.AddReceiptRequestProto{Receipt: nil}, "admin"); err != billing.ErrNilReceipt {
		t.Errorf("expected ErrNilReceipt for nil inner receipt, got %v", err)
	}

	// 2. AttachReceiptPDF errors
	if _, err := client.AttachReceiptPDF(ctx, "", []byte("pdf"), "file.pdf"); err == nil {
		t.Error("expected error for empty receiptID in AttachReceiptPDF")
	}
	if _, err := client.AttachReceiptPDF(ctx, "rec-1", nil, "file.pdf"); err == nil {
		t.Error("expected error for empty pdfContent in AttachReceiptPDF")
	}
	if _, err := client.AttachReceiptPDF(ctx, "non-existent", []byte("pdf"), ""); err != billing.ErrReceiptNotFound {
		t.Errorf("expected ErrReceiptNotFound, got %v", err)
	}

	// 3. GetReceiptPDF errors
	if _, _, err := client.GetReceiptPDF(ctx, "non-existent"); err != billing.ErrReceiptNotFound {
		t.Errorf("expected ErrReceiptNotFound, got %v", err)
	}

	// 4. GetReceiptDownloadLink errors
	if _, _, err := client.GetReceiptDownloadLink(ctx, "non-existent", 3600); err != billing.ErrReceiptNotFound {
		t.Errorf("expected ErrReceiptNotFound, got %v", err)
	}

	// 5. Add receipt without PDF, then check GetReceiptPDF and GetReceiptDownloadLink
	r, err := client.AddReceipt(ctx, &billing.AddReceiptRequestProto{
		Receipt: &billing.PaymentReceiptProto{
			Description: "No PDF receipt",
		},
	}, "admin")
	if err != nil {
		t.Fatalf("AddReceipt failed: %v", err)
	}
	if _, _, err := client.GetReceiptPDF(ctx, r.Id); err == nil {
		t.Error("expected error for receipt without attached PDF")
	}
	if _, _, err := client.GetReceiptDownloadLink(ctx, r.Id, 3600); err == nil {
		t.Error("expected error for receipt without attached PDF in GetReceiptDownloadLink")
	}

	// 6. Non-existent receipt lookup
	if client.GetReceipt("missing-receipt-id") != nil {
		t.Error("expected nil for missing receipt ID")
	}

	// 7. Schema minor & proto helpers
	if client.GetSchemaMinorVersion() != billing.SchemaMinor {
		t.Errorf("expected schema minor %d, got %d", billing.SchemaMinor, client.GetSchemaMinorVersion())
	}
	if err := client.ReadFromProto(nil); err != nil {
		t.Errorf("ReadFromProto(nil) failed: %v", err)
	}
	var doc billing.BillingProto
	client.StampVersion(&doc, 2, "commit-sha")
	if doc.SchemaMinorVersion != 2 || doc.LastModifiedByCommit != "commit-sha" || doc.TenantId != 4001 {
		t.Errorf("StampVersion failed: %+v", &doc)
	}
}

func TestBilling_ProtobufGetters(t *testing.T) {
	r := &billing.PaymentReceiptProto{
		Id:               "rec-1",
		InvoiceId:        "inv-1",
		AmountCents:      1000,
		Currency:         "EUR",
		Description:      "desc",
		ProductId:        "prod",
		ReceiptObjectKey: "obj/key",
		PdfFilename:      "rec.pdf",
		SizeBytes:        500,
		CreatedAtUnixMs:  123,
	}
	_ = r.GetId()
	_ = r.GetInvoiceId()
	_ = r.GetAmountCents()
	_ = r.GetCurrency()
	_ = r.GetDescription()
	_ = r.GetProductId()
	_ = r.GetReceiptObjectKey()
	_ = r.GetPdfFilename()
	_ = r.GetSizeBytes()
	_ = r.GetCreatedAtUnixMs()
	_ = r.String()
	_, _ = r.Descriptor()
	_ = r.ProtoReflect()
	r.ProtoMessage()

	req := &billing.AddReceiptRequestProto{
		Receipt:     r,
		PdfContent:  []byte("content"),
		PdfFilename: "file.pdf",
	}
	_ = req.GetReceipt()
	_ = req.GetPdfContent()
	_ = req.GetPdfFilename()
	_ = req.String()
	_, _ = req.Descriptor()
	_ = req.ProtoReflect()
	req.ProtoMessage()

	bp := &billing.BillingProto{
		TenantId:             123,
		SchemaMinorVersion:   1,
		LastModifiedByCommit: "commit",
		UpdatedAtUnixMs:      456,
		Receipts:             []*billing.PaymentReceiptProto{r},
	}
	_ = bp.GetTenantId()
	_ = bp.GetSchemaMinorVersion()
	_ = bp.GetLastModifiedByCommit()
	_ = bp.GetUpdatedAtUnixMs()
	_ = bp.GetReceipts()
	_ = bp.String()
	_, _ = bp.Descriptor()
	_ = bp.ProtoReflect()
	bp.ProtoMessage()

	// Nil getters
	var nilR *billing.PaymentReceiptProto
	_ = nilR.GetId()
	_ = nilR.GetInvoiceId()
	_ = nilR.GetAmountCents()
	_ = nilR.GetCurrency()
	_ = nilR.GetDescription()
	_ = nilR.GetProductId()
	_ = nilR.GetReceiptObjectKey()
	_ = nilR.GetPdfFilename()
	_ = nilR.GetSizeBytes()
	_ = nilR.GetCreatedAtUnixMs()

	var nilReq *billing.AddReceiptRequestProto
	_ = nilReq.GetReceipt()
	_ = nilReq.GetPdfContent()
	_ = nilReq.GetPdfFilename()

	var nilBp *billing.BillingProto
	_ = nilBp.GetTenantId()
	_ = nilBp.GetSchemaMinorVersion()
	_ = nilBp.GetLastModifiedByCommit()
	_ = nilBp.GetUpdatedAtUnixMs()
	_ = nilBp.GetReceipts()
}
