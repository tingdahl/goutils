package storage

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tingdahl/goutils/config"
)

// Standard MIME content types
const (
	ContentTypeApplicationJSON        = "application/json"
	ContentTypeApplicationProtobuf    = "application/x-protobuf"
	ContentTypeApplicationJavaScript  = "application/javascript"
	ContentTypeTextHTML               = "text/html"
	ContentTypeTextPlain              = "text/plain"
	ContentTypeApplicationOctetStream = "application/octet-stream"
)

// IsZstdKey returns true if the object key indicates a Zstandard-compressed file (.zst extension).
func IsZstdKey(object string) bool {
	return strings.HasSuffix(object, ".zst")
}

// IsBrotliKey is maintained for backward compatibility.
func IsBrotliKey(object string) bool {
	return strings.HasSuffix(object, ".br")
}

// ContentTypeFromKey infers the MIME content type from an object key, stripping any .zst or .br extension if present.
func ContentTypeFromKey(object string) string {
	name := strings.TrimSuffix(strings.TrimSuffix(object, ".zst"), ".br")
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
	GetCurrentRevision(ctx context.Context, object string) (string, error)
	WriteObject(ctx context.Context, object string, data []byte) (string, error)
	WriteRawObject(ctx context.Context, object string, data []byte) (string, error)
	WriteObjectIfRevisionMatch(ctx context.Context, object string, data []byte, revision string) (string, error)
	ReadObject(ctx context.Context, object string) ([]byte, string, error)
	ReadRawObject(ctx context.Context, object string) ([]byte, string, error)
	GetObjectLink(ctx context.Context, object string, duration int, IPAddress string) (string, error)
	GetUploadLink(ctx context.Context, object string, duration int, contentType string) (string, error)
	DeleteObject(ctx context.Context, object string) error
	ListPrefixes(ctx context.Context, prefix string, delimiter string) ([]string, error)
	ListObjects(ctx context.Context, prefix string) ([]StorageObject, error)
}

const NoRevision = ""

var (
	RevisionWriteError = errors.New("Failed to write to storage because the data has been updated by someone else.")
	WriteFailedError   = errors.New("Failed to write to storage. Please try again.")
)

// Constructor is the signature for storage client factory functions.
type Constructor func(opts map[string]string) (StorageClient, error)

var (
	Environment string
	constructor Constructor
)

// NewStorageClient creates a new StorageClient using the registered constructor.
// If option maps are provided, they are merged in order and passed to the constructor.
// If no options are provided, the constructor uses environment/config defaults.
func NewStorageClient(opts ...map[string]string) (StorageClient, error) {
	if constructor == nil {
		return nil, errors.New("storage client constructor not initialized")
	}
	merged := make(map[string]string)
	for _, opt := range opts {
		for k, v := range opt {
			merged[k] = v
		}
	}
	return constructor(merged)
}

// SetStorageConstructor sets the factory function for creating StorageClient instances.
func SetStorageConstructor(c Constructor) {
	constructor = c
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

// DriverInit is the function signature for storage driver initializers.
type DriverInit func() error

var (
	driversMu         sync.RWMutex
	registeredDrivers = make(map[string]DriverInit)
)

// RegisterDriver registers a storage driver by name (case-insensitive).
func RegisterDriver(name string, initFn DriverInit) {
	driversMu.Lock()
	defer driversMu.Unlock()
	registeredDrivers[strings.ToLower(name)] = initFn
}

// GetDriver returns the registered DriverInit for the given name if present.
func GetDriver(name string) (DriverInit, bool) {
	driversMu.RLock()
	defer driversMu.RUnlock()
	fn, ok := registeredDrivers[strings.ToLower(name)]
	return fn, ok
}

// AvailableDrivers returns a sorted list of registered storage driver names.
func AvailableDrivers() []string {
	driversMu.RLock()
	defer driversMu.RUnlock()
	var names []string
	for name := range registeredDrivers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// InitDriver initializes the specified storage driver by name.
func InitDriver(name string) error {
	fn, ok := GetDriver(name)
	if !ok {
		return fmt.Errorf("storage driver %q is not compiled into this binary (available in this build: %v)", name, AvailableDrivers())
	}
	return fn()
}

// InitFromEnv initializes the storage driver specified by environment variables
// (checked in order: STORAGE_PROVIDER, STORAGE_BACKEND, PUBLIC_CLOUD).
// If no environment variable is set and only one distinct driver is available, it defaults to it.
func InitFromEnv() error {
	drivers := AvailableDrivers()
	if len(drivers) == 0 {
		return errors.New("no storage driver compiled into this binary")
	}

	var providerName string
	for _, key := range []string{"STORAGE_PROVIDER", "STORAGE_BACKEND", "PUBLIC_CLOUD"} {
		if val := strings.TrimSpace(config.GetConfigString(key)); val != "" {
			providerName = val
			break
		}
	}

	if providerName == "" {
		if len(drivers) == 1 {
			providerName = drivers[0]
			} else {
			// Check if all registered drivers point to the same init function (aliases like gcp/gcs)
			driversMu.RLock()
			var firstFn DriverInit
			same := true
			for _, fn := range registeredDrivers {
				if firstFn == nil {
					firstFn = fn
				} else if reflect.ValueOf(firstFn).Pointer() != reflect.ValueOf(fn).Pointer() {
					same = false
					break
				}
			}
			driversMu.RUnlock()

			if same && len(drivers) > 0 {
				providerName = drivers[0]
					} else {
				return fmt.Errorf("storage driver must be configured via STORAGE_PROVIDER, STORAGE_BACKEND, or PUBLIC_CLOUD (available drivers in this build: %v)", drivers)
			}
		}
	}

	return InitDriver(providerName)
}
