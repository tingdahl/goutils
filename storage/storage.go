package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tingdahl/goutils/config"
)

// Standard MIME content types
const (
	ContentTypeApplicationJSON       = "application/json"
	ContentTypeApplicationProtobuf   = "application/x-protobuf"
	ContentTypeApplicationJavaScript = "application/javascript"
	ContentTypeTextHTML              = "text/html"
	ContentTypeTextPlain             = "text/plain"
	ContentTypeApplicationOctetStream = "application/octet-stream"
)

// IsBrotliKey returns true if the object key indicates a Brotli-compressed file (.br extension).
func IsBrotliKey(object string) bool {
	return strings.HasSuffix(object, ".br")
}

// ContentTypeFromKey infers the MIME content type from an object key, stripping any .br extension if present.
func ContentTypeFromKey(object string) string {
	name := strings.TrimSuffix(object, ".br")
	switch {
	case strings.HasSuffix(name, ".pb"):
		return ContentTypeApplicationProtobuf
	case strings.HasSuffix(name, ".json"):
		return ContentTypeApplicationJSON
	case strings.HasSuffix(name, ".pdf"):
		return "application/pdf"
	case strings.HasSuffix(name, ".png"):
		return "image/png"
	case strings.HasSuffix(name, ".jpg"), strings.HasSuffix(name, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(name, ".html"):
		return "text/html"
	case strings.HasSuffix(name, ".txt"):
		return "text/plain"
	case strings.HasSuffix(name, ".js"), strings.HasSuffix(name, ".mjs"):
		return ContentTypeApplicationJavaScript
	case strings.HasSuffix(name, ".css"):
		return "text/css"
	case strings.HasSuffix(name, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(name, ".wasm"):
		return "application/wasm"
	default:
		return ContentTypeApplicationOctetStream
	}
}

// StorageObject represents metadata for a stored object.
type StorageObject struct {
	Key          string
	LastModified time.Time
}

// StorageClient defines the common interface for cloud object storage (S3, GCS).
type StorageClient interface {
	GetCurrentRevision(ctx context.Context, bucket string, object string) (string, error)
	WriteObject(ctx context.Context, bucket string, object string, data []byte) (string, error)
	WriteRawObject(ctx context.Context, bucket string, object string, data []byte) (string, error)
	WriteObjectIfRevisionMatch(ctx context.Context, bucket string, object string, data []byte, revision string) (string, error)
	ReadObject(ctx context.Context, bucket string, object string) ([]byte, string, error)
	ReadRawObject(ctx context.Context, bucket string, object string) ([]byte, string, error)
	GetObjectLink(ctx context.Context, bucket string, object string, duration int, IPAddress string) (string, error)
	GetUploadLink(ctx context.Context, bucket string, object string, duration int, contentType string) (string, error)
	DeleteObject(ctx context.Context, bucket string, object string) error
	ListPrefixes(ctx context.Context, bucket string, prefix string, delimiter string) ([]string, error)
	ListObjects(ctx context.Context, bucket string, prefix string) ([]StorageObject, error)
}

const NoRevision = ""

var (
	RevisionWriteError = errors.New("Failed to write to storage because the data has been updated by someone else.")
	WriteFailedError   = errors.New("Failed to write to storage. Please try again.")
)

var (
	Environment string
	BucketName  string
	constructor func() (StorageClient, error)
)

// NewStorageClient creates a new StorageClient using the registered constructor.
func NewStorageClient() (StorageClient, error) {
	if constructor == nil {
		return nil, errors.New("storage client constructor not initialized")
	}
	return constructor()
}

// SetStorageConstructor sets the factory function for creating StorageClient instances.
func SetStorageConstructor(c func() (StorageClient, error)) {
	constructor = c
}

// DetermineBucketName retrieves the bucket name from config (S3_APPDATA_NAME).
func DetermineBucketName() string {
	return config.Config().GetS3AppDataBucket()
}

// Init initializes the default bucket name from configuration.
func Init() error {
	if BucketName == "" {
		BucketName = DetermineBucketName()
	}
	if BucketName == "" {
		return errors.New("environment variable S3_APPDATA_NAME is required but not set")
	}
	return nil
}

// CheckHealth verifies that the storage client constructor is initialized and can produce a valid client.
func CheckHealth(ctx context.Context) error {
	if constructor == nil {
		return errors.New("storage client constructor not initialized")
	}
	client, err := NewStorageClient()
	if err != nil {
		return fmt.Errorf("failed to create storage client: %w", err)
	}
	if client == nil {
		return errors.New("storage client is nil")
	}
	return nil
}
