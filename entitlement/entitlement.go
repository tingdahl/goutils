package entitlement

//go:generate protoc --go_out=. --go_opt=paths=source_relative entitlement.v1.proto

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tingdahl/goutils/storage"
	"github.com/tingdahl/goutils/storage/docstore"
	"google.golang.org/protobuf/proto"
)

// Init initializes the entitlement repository for the given storage client and bucket.
func Init(store storage.StorageClient, bucket string, prefix string) error {
	if store == nil {
		return errors.New("storage client cannot be nil")
	}
	if bucket == "" {
		return errors.New("bucket name cannot be empty")
	}

	entitlementInitOnce.Do(func() {
		entitlementPrefix = prefix
		entitlementStore = store
		entitlementBucket = bucket
		entitlementRepo = docstore.NewRepository[*EntitlementClient](entitlementClientFactory)
	})
	return nil
}

// GetEntitlementClient retrieves the cached EntitlementClient for the specified tenant ID.
func GetEntitlementClient(ctx context.Context, tenantID int64) (*EntitlementClient, error) {
	if entitlementRepo == nil {
		return nil, errors.New("entitlement package not initialized: call entitlement.Init() first")
	}
	if tenantID <= 0 {
		return nil, errors.New("tenant ID must be greater than zero")
	}

	client, err := entitlementRepo.Get(ctx, tenantEntitlementPath(tenantID))
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

// EntitlementClient provides access to entitlement data for a tenant.
type EntitlementClient struct {
	docstore.Document
	tenantID int64
	data     EntitlementProto
}

func (c *EntitlementClient) TenantID() int64 {
	return c.tenantID
}

func (c *EntitlementClient) Dimensions() map[int32]*EntitlementDimensionProto {
	c.Rwlock.RLock()
	defer c.Rwlock.RUnlock()
	return c.data.Dimensions
}

// For safety, never remove dimensions. If business logic stops using it, it will sit there forever.
func (c *EntitlementClient) AddDimensions(ctx context.Context, dimensions map[int32]*EntitlementDimensionProto) error {
	return c.Update(ctx, func(m proto.Message) error {
		msg := m.(*EntitlementProto)
		if msg.Dimensions == nil {
			msg.Dimensions = make(map[int32]*EntitlementDimensionProto)
		}
		for dim_id, d := range dimensions {
			if _, ok := msg.Dimensions[dim_id]; ok {
				return errors.New("dimension already exists")
			}
			msg.Dimensions[dim_id] = d
		}
		return nil
	})
}

func (c *EntitlementClient) RemoveDimensions(ctx context.Context, dimensionIDs []int32) error {
	return c.Update(ctx, func(m proto.Message) error {
		msg := m.(*EntitlementProto)
		for _, dimID := range dimensionIDs {
			if _, ok := msg.Dimensions[dimID]; !ok {
				return errors.New("dimension not found")
			}
			delete(msg.Dimensions, dimID)
		}
		return nil
	})
}

func (c *EntitlementClient) Transactions() []*EntitlementTransactionProto {
	c.Rwlock.RLock()
	defer c.Rwlock.RUnlock()
	return c.data.Transactions
}

func (c *EntitlementClient) GetCurrentEntitlementValues() map[int32]int64 {
	c.Rwlock.RLock()
	defer c.Rwlock.RUnlock()

	values := map[int32]int64{}
	types := map[int32]EntitlementDimensionType{}
	for dimId, dim := range c.data.Dimensions {
		values[dimId] = 0
		types[dimId] = dim.Type
	}

	//Transactions are sorted by effective_at_unix_ms when they are written so we can just iterate
	transactions := c.data.Transactions

	for _, tx := range transactions {
		for dimId, val := range tx.DimensionValues {
			if _, ok := types[dimId]; !ok {
				slog.Error("Dimension found on transaction but ont in entitlement", "tenant_id", c.TenantID(), "dimension_id", dimId)
				continue
			}

			dimType := types[dimId]
			switch dimType {
			case EntitlementDimensionType_ENTITLEMENT_DIMENSION_TYPE_INCREMENTAL:
				values[dimId] += val
			case EntitlementDimensionType_ENTITLEMENT_DIMENSION_TYPE_LAST_VALUE:
				values[dimId] = val
			}
		}
	}

	return values
}

func (t *EntitlementTransactionProto) validate() error {
	if t == nil {
		return errors.New("transaction cannot be nil")
	}
	if t.EffectiveAtUnixMs <= 0 {
		return errors.New("effective_at_unix_ms must be set")
	}
	if len(t.DimensionValues) == 0 {
		return errors.New("dimension_values cannot be empty")
	}
	if t.Description == "" {
		return errors.New("description cannot be empty")
	}
	if t.TransactionType == EntitlementTransactionType_ENTITLEMENT_TRANSACTION_TYPE_LEASE {
		if t.ExpiresAtUnixMs <= 0 {
			return errors.New("expires_at_unix_ms must be set")
		}
	}

	return nil
}

func pruneExpiredTransactions(transactions []*EntitlementTransactionProto) []*EntitlementTransactionProto {
	var keep = []*EntitlementTransactionProto{}
	for _, tx := range transactions {
		if tx.TransactionType != EntitlementTransactionType_ENTITLEMENT_TRANSACTION_TYPE_LEASE {
			keep = append(keep, tx)
			continue
		}

		if tx.ExpiresAtUnixMs > time.Now().UnixMilli() {
			keep = append(keep, tx)
			continue
		}
	}

	return keep
}


func (c *EntitlementClient) AddTransaction(ctx context.Context, transaction *EntitlementTransactionProto) error {
	if err := transaction.validate(); err != nil {
		return err
	}

	return c.Update(ctx, func(m proto.Message) error {
		msg := m.(*EntitlementProto)
		if transaction.CreatedAtUnixMs <= 0 {
			transaction.CreatedAtUnixMs = time.Now().UnixMilli()
		}
		if transaction.EffectiveAtUnixMs <= 0 {
			transaction.EffectiveAtUnixMs = transaction.CreatedAtUnixMs
		}

		// Allocate sequential id starting from 1 if not pre-set
		if transaction.TransactionId <= 0 {
			var maxId int32 = 0
			for _, item := range msg.Transactions {
				if item.TransactionId > maxId {
					maxId = item.TransactionId
				}
			}
			transaction.TransactionId = maxId + 1
		}

		msg.Transactions = pruneExpiredTransactions(append(msg.Transactions, transaction))
		sort.SliceStable(msg.Transactions, func(i, j int) bool {
			return msg.Transactions[i].EffectiveAtUnixMs < msg.Transactions[j].EffectiveAtUnixMs
		})
		return nil
	})
}

func (c *EntitlementClient) RemoveTransaction(ctx context.Context, transactionID int32) error {
	return c.Update(ctx, func(m proto.Message) error {
		msg := m.(*EntitlementProto)
		for i, item := range msg.Transactions {
			if item.TransactionId == transactionID {
				msg.Transactions = pruneExpiredTransactions(append(msg.Transactions[:i], msg.Transactions[i+1:]...))
				return nil
			}
		}
		return errors.New("transaction not found")
	})
}

// tenantEntitlementPath returns the storage object path for a given tenant ID.
func tenantEntitlementPath(tenantID int64) string {
	prefix := strings.Trim(entitlementPrefix, "/")
	if prefix == "" {
		return fmt.Sprintf("%d/%s", tenantID, DbObjectName)
	}
	return fmt.Sprintf("%s/%d/%s", prefix, tenantID, DbObjectName)
}

const (
	DbObjectName = "entitlement.v1.pb.br"
	SchemaMinor  int32 = 0
)

var (
	entitlementInitOnce sync.Once
	entitlementRepo     *docstore.Repository[*EntitlementClient]
	entitlementStore    storage.StorageClient
	entitlementBucket   string
	entitlementPrefix   string
)

func entitlementClientFactory(ctx context.Context, objectPath string) (*EntitlementClient, error) {
	if entitlementStore == nil {
		return nil, errors.New("storage client cannot be nil")
	}
	if entitlementBucket == "" {
		return nil, errors.New("bucket name cannot be empty")
	}
	if objectPath == "" {
		return nil, errors.New("object path cannot be empty")
	}

	client := &EntitlementClient{}
	client.Document = docstore.Document{
		Client:               client,
		Storage:              entitlementStore,
		BucketName:           entitlementBucket,
		ObjectName:           objectPath,
		ProtoMsg:             &client.data,
		SupportedSchemaMinor: SchemaMinor,
	}

	if err := client.DoLoad(); err != nil {
		return nil, fmt.Errorf("failed to load entitlement database from storage: %w", err)
	}

	return client, nil
}

func (c *EntitlementClient) ReadFromProto(data []byte) error {
	proto.Reset(&c.data)
	if data == nil {
		return nil
	}
	return proto.Unmarshal(data, &c.data)
}

func (c *EntitlementClient) GetSchemaMinorVersion() int32 {
	return c.data.SchemaMinorVersion
}

func (c *EntitlementClient) StampVersion(msg proto.Message, schemaMinor int32, commit string) {
	doc := msg.(*EntitlementProto)
	doc.TenantId = c.tenantID
	doc.SchemaMinorVersion = schemaMinor
	doc.LastModifiedByCommit = commit
}
