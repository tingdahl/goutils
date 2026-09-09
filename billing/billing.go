package billing

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tingdahl/goutils/storage"
	"github.com/tingdahl/goutils/storage/docstore"
	"google.golang.org/protobuf/proto"
)

const (
	SchemaMinor    int32  = 0
	ReceiptsPrefix string = "billing/receipts"
	DbObjectName   string = "billing.v1.pb"
)

var (
	ErrEntityNotFound  = errors.New("entity billing record not found")
	ErrReceiptNotFound = errors.New("payment receipt not found")
	ErrNilReceipt      = errors.New("receipt cannot be nil")
	initOnce           sync.Once
	billingClient      *BillingClient
)

type BillingClient struct {
	docstore.Document
	data BillingProto
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
	doc.SchemaMinorVersion = schemaMinor
	doc.LastModifiedByCommit = commit
}

// NewBillingClient creates a new BillingClient using the provided storage client and bucket name.
func NewBillingClient(store storage.StorageClient, bucket string) (*BillingClient, error) {
	if store == nil {
		return nil, errors.New("storage client cannot be nil")
	}
	if bucket == "" {
		return nil, errors.New("bucket name cannot be empty")
	}

	client := &BillingClient{}
	client.Document = docstore.Document{
		Client:               client,
		Storage:              store,
		BucketName:           bucket,
		ObjectName:           DbObjectName,
		ProtoMsg:             &client.data,
		SupportedSchemaMinor: SchemaMinor,
	}

	if err := client.DoLoad(); err != nil {
		return nil, fmt.Errorf("failed to load billing database from storage: %w", err)
	}

	return client, nil
}

// CreateBillingClient creates a BillingClient using the globally configured storage client and default bucket.
func CreateBillingClient(ctx context.Context) (*BillingClient, error) {
	var initErr error
	initOnce.Do(func() {
		if err := storage.Init(); err != nil {
			initErr = fmt.Errorf("failed to initialize storage: %w", err)
			return
		}

		store, err := storage.NewStorageClient()
		if err != nil {
			initErr = fmt.Errorf("failed to create storage client: %w", err)
			return
		}

		client, err := NewBillingClient(store, storage.BucketName)
		if err != nil {
			initErr = err
			return
		}
		billingClient = client
	})

	if initErr != nil {
		return nil, initErr
	}
	if billingClient == nil {
		return nil, errors.New("billing client not initialized")
	}
	return billingClient, nil
}

// GetEntityBilling retrieves a copy of billing records for a given entity (tenant/company) ID.
func (s *BillingClient) GetEntityBilling(entityID int64) *EntityBillingProto {
	s.Rwlock.RLock()
	defer s.Rwlock.RUnlock()

	if s.data.Entities != nil {
		if entity, ok := s.data.Entities[entityID]; ok && entity != nil {
			return proto.Clone(entity).(*EntityBillingProto)
		}
	}

	return &EntityBillingProto{
		EntityId: entityID,
		Receipts: []*PaymentReceiptProto{},
	}
}

// GetAllEntityBilling retrieves copies of all entity billing records.
func (s *BillingClient) GetAllEntityBilling() map[int64]*EntityBillingProto {
	s.Rwlock.RLock()
	defer s.Rwlock.RUnlock()

	result := make(map[int64]*EntityBillingProto, len(s.data.Entities))
	for entityID, entity := range s.data.Entities {
		if entity != nil {
			result[entityID] = proto.Clone(entity).(*EntityBillingProto)
		}
	}
	return result
}

// ListReceipts returns all receipts recorded for a given entity ID.
func (s *BillingClient) ListReceipts(entityID int64) []*PaymentReceiptProto {
	entity := s.GetEntityBilling(entityID)
	if entity == nil || len(entity.Receipts) == 0 {
		return []*PaymentReceiptProto{}
	}
	return entity.Receipts
}

// GetReceipt returns a receipt by ID or InvoiceId for an entity.
func (s *BillingClient) GetReceipt(entityID int64, receiptID string) *PaymentReceiptProto {
	entity := s.GetEntityBilling(entityID)
	for _, r := range entity.Receipts {
		if r.Id == receiptID || r.InvoiceId == receiptID {
			return r
		}
	}
	return nil
}

// AddReceipt records a payment receipt for an entity, saves optional PDF bytes to object storage, and returns the saved receipt.
func (s *BillingClient) AddReceipt(ctx context.Context, entityID int64, req *AddReceiptRequestProto, userId string) (*PaymentReceiptProto, error) {
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
		objectKey := fmt.Sprintf("%s/%d/%s.pdf", ReceiptsPrefix, entityID, receipt.Id)
		_, err := s.Storage.WriteRawObject(ctx, s.BucketName, objectKey, req.PdfContent)
		if err != nil {
			return nil, fmt.Errorf("failed to write receipt PDF to storage: %w", err)
		}

		receipt.ReceiptObjectKey = objectKey
		receipt.SizeBytes = int64(len(req.PdfContent))
	}

	// 2. Append receipt to billing database
	err := s.Update(ctx, func(msg proto.Message) error {
		doc := msg.(*BillingProto)
		if doc.Entities == nil {
			doc.Entities = make(map[int64]*EntityBillingProto)
		}

		now := time.Now().UnixMilli()
		entity, exists := doc.Entities[entityID]
		if !exists || entity == nil {
			entity = &EntityBillingProto{
				EntityId:        entityID,
				UpdatedAtUnixMs: now,
				Receipts:        []*PaymentReceiptProto{receipt},
			}
			doc.Entities[entityID] = entity
			return nil
		}

		entity.UpdatedAtUnixMs = now
		entity.Receipts = append(entity.Receipts, receipt)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return receipt, nil
}

// GetReceiptPDF retrieves the raw PDF bytes and metadata for a specific receipt of an entity.
func (s *BillingClient) GetReceiptPDF(ctx context.Context, entityID int64, receiptID string) ([]byte, *PaymentReceiptProto, error) {
	receipt := s.GetReceipt(entityID, receiptID)
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
func (s *BillingClient) GetReceiptDownloadLink(ctx context.Context, entityID int64, receiptID string, validitySeconds int) (string, *PaymentReceiptProto, error) {
	receipt := s.GetReceipt(entityID, receiptID)
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
