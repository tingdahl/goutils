package billing

//go:generate protoc --go_out=. --go_opt=paths=source_relative billing.v1.proto

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tingdahl/goutils/storage"
	"github.com/tingdahl/goutils/storage/docstore"
	"google.golang.org/protobuf/proto"
)

// Init initializes the billing repository for the given storage client and bucket.
func Init(store storage.StorageClient, bucket string, prefix string) error {
	if store == nil {
		return errors.New("storage client cannot be nil")
	}
	if bucket == "" {
		return errors.New("bucket name cannot be empty")
	}

	billingInitOnce.Do(func() {
		billingPrefix = prefix
		billingStore = store
		billingBucket = bucket
		billingRepo = docstore.NewRepository(billingClientFactory)
	})
	return nil
}

// GetBillingClient retrieves the cached BillingClient for the specified tenant ID.
func GetBillingClient(ctx context.Context, tenantID int64) (*BillingClient, error) {
	if billingRepo == nil {
		return nil, errors.New("billing package not initialized: call billing.Init() first")
	}
	if tenantID <= 0 {
		return nil, errors.New("tenant ID must be greater than zero")
	}

	client, err := billingRepo.Get(ctx, TenantBillingPath(tenantID))
	if err != nil {
		return nil, err
	}

	client.Rwlock.RLock()
	existingTenantID := client.data.TenantId
	client.Rwlock.RUnlock()

	if existingTenantID != 0 && existingTenantID != tenantID {
		return nil, fmt.Errorf("tenant ID mismatch: expected %d, got %d", tenantID, existingTenantID)
	}

	client.tenantID = tenantID
	return client, nil
}

// TenantBillingPath returns the storage object path for a given tenant ID.
func TenantBillingPath(tenantID int64) string {
	prefix := strings.Trim(billingPrefix, "/")
	if prefix == "" {
		return fmt.Sprintf("%d/%s", tenantID, DbObjectName)
	}
	return fmt.Sprintf("%s/%d/%s", prefix, tenantID, DbObjectName)
}

// BillingClient provides access to billing data and payment receipts for a tenant.
type BillingClient struct {
	docstore.Document
	tenantID int64
	data     BillingProto
}

// TenantID returns the tenant ID associated with this billing document.
func (s *BillingClient) TenantID() int64 {
	return s.tenantID
}

// ListReceipts returns all receipts recorded for this tenant.
func (s *BillingClient) ListReceipts() []*PaymentReceiptProto {
	s.Rwlock.RLock()
	defer s.Rwlock.RUnlock()

	if len(s.data.Receipts) == 0 {
		return []*PaymentReceiptProto{}
	}

	result := make([]*PaymentReceiptProto, len(s.data.Receipts))
	for i, r := range s.data.Receipts {
		result[i] = proto.Clone(r).(*PaymentReceiptProto)
	}
	return result
}

// GetReceipt returns a receipt by ID or InvoiceId.
func (s *BillingClient) GetReceipt(receiptID string) *PaymentReceiptProto {
	s.Rwlock.RLock()
	defer s.Rwlock.RUnlock()

	for _, r := range s.data.Receipts {
		if r.Id == receiptID || r.InvoiceId == receiptID {
			return proto.Clone(r).(*PaymentReceiptProto)
		}
	}
	return nil
}

// AddReceipt records a payment receipt for the tenant, saves optional PDF bytes to object storage, and returns the saved receipt.
func (s *BillingClient) AddReceipt(ctx context.Context, req *AddReceiptRequestProto, userId string) (*PaymentReceiptProto, error) {
	if req == nil || req.Receipt == nil {
		return nil, ErrNilReceipt
	}

	receipt := proto.Clone(req.Receipt).(*PaymentReceiptProto)
	if receipt.Id == "" {
		receipt.Id = uuid.New().String()
	}
	if receipt.CreatedAtUnixMs <= 0 {
		receipt.CreatedAtUnixMs = time.Now().UnixMilli()
	}

	filename := req.PdfFilename
	if filename == "" {
		filename = fmt.Sprintf("receipt-%s.pdf", receipt.Id)
	}
	receipt.PdfFilename = filename

	// 1. If PDF bytes are provided, store via storage client under ReceiptsPrefix
	if len(req.PdfContent) > 0 {
		tenantID := s.TenantID()
		objectKey := fmt.Sprintf("%s/%d/%s.pdf", ReceiptsPrefix, tenantID, receipt.Id)
		_, err := s.Storage.WriteRawObject(ctx, s.BucketName, objectKey, req.PdfContent)
		if err != nil {
			return nil, fmt.Errorf("failed to write receipt PDF to storage: %w", err)
		}

		receipt.ReceiptObjectKey = objectKey
		receipt.SizeBytes = int64(len(req.PdfContent))
	}

	// 2. Append receipt to tenant's billing document
	err := s.Update(ctx, func(msg proto.Message) error {
		doc := msg.(*BillingProto)
		doc.UpdatedAtUnixMs = time.Now().UnixMilli()
		doc.Receipts = append(doc.Receipts, receipt)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return receipt, nil
}

// GetReceiptPDF retrieves the raw PDF bytes and metadata for a specific receipt.
func (s *BillingClient) GetReceiptPDF(ctx context.Context, receiptID string) ([]byte, *PaymentReceiptProto, error) {
	receipt := s.GetReceipt(receiptID)
	if receipt == nil {
		return nil, nil, ErrReceiptNotFound
	}

	if receipt.ReceiptObjectKey == "" {
		return nil, nil, errors.New("no PDF file attached to this receipt")
	}

	content, _, err := s.Storage.ReadRawObject(ctx, s.BucketName, receipt.ReceiptObjectKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read receipt file content: %w", err)
	}

	return content, receipt, nil
}

// GetReceiptDownloadLink returns a pre-signed storage URL for a specific receipt.
func (s *BillingClient) GetReceiptDownloadLink(ctx context.Context, receiptID string, validitySeconds int) (string, *PaymentReceiptProto, error) {
	receipt := s.GetReceipt(receiptID)
	if receipt == nil {
		return "", nil, ErrReceiptNotFound
	}

	if receipt.ReceiptObjectKey == "" {
		return "", nil, errors.New("no PDF file attached to this receipt")
	}

	url, err := s.Storage.GetObjectLink(ctx, s.BucketName, receipt.ReceiptObjectKey, validitySeconds, "")
	if err != nil {
		return "", nil, fmt.Errorf("failed to generate signed download link: %w", err)
	}

	return url, receipt, nil
}

// Exported constants and errors.
const (
	DbObjectName   string = "billing.v1.pb.br"
	ReceiptsPrefix string = "billing/receipts"
	SchemaMinor    int32  = 0
)

var (
	ErrReceiptNotFound = errors.New("payment receipt not found")
	ErrNilReceipt      = errors.New("receipt cannot be nil")
)

// --- Internal Implementation Details & Package State ---

var (
	billingInitOnce sync.Once
	billingRepo     *docstore.Repository[*BillingClient]
	billingStore    storage.StorageClient
	billingBucket   string
	billingPrefix   string
)

func billingClientFactory(ctx context.Context, objectPath string) (*BillingClient, error) {
	if billingStore == nil {
		return nil, errors.New("storage client cannot be nil")
	}
	if billingBucket == "" {
		return nil, errors.New("bucket name cannot be empty")
	}
	if objectPath == "" {
		return nil, fmt.Errorf("object path cannot be empty")
	}

	client := &BillingClient{}
	client.Document = docstore.Document{
		Client:               client,
		Storage:              billingStore,
		BucketName:           billingBucket,
		ObjectName:           objectPath,
		ProtoMsg:             &client.data,
		SupportedSchemaMinor: SchemaMinor,
	}

	if err := client.DoLoad(); err != nil {
		return nil, fmt.Errorf("failed to load billing database from storage: %w", err)
	}

	return client, nil
}

func (s *BillingClient) ReadFromProto(data []byte) error {
	proto.Reset(&s.data)
	if data == nil {
		return nil
	}
	return proto.Unmarshal(data, &s.data)
}

func (s *BillingClient) GetSchemaMinorVersion() int32 {
	return s.data.SchemaMinorVersion
}

func (s *BillingClient) StampVersion(msg proto.Message, schemaMinor int32, commit string) {
	doc := msg.(*BillingProto)
	doc.TenantId = s.tenantID
	doc.SchemaMinorVersion = schemaMinor
	doc.LastModifiedByCommit = commit
}
