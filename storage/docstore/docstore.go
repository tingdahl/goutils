package docstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tingdahl/goutils/storage"
	"github.com/tingdahl/goutils/version"

	"github.com/aws/smithy-go"
	"google.golang.org/protobuf/proto"
)

var (
	// ErrNewerSchemaVersionWriteForbidden is returned when attempting to overwrite a document created by a newer schema version.
	ErrNewerSchemaVersionWriteForbidden = errors.New("cannot write to storage because the document was modified by a newer schema version")
)

// DocumentClient is implemented by domain document structures to handle unmarshaling and version stamping.
type DocumentClient interface {
	ReadFromProto([]byte) error
	GetSchemaMinorVersion() int32
	StampVersion(msg proto.Message, schemaMinor int32, commit string)
}

// DocumentHolder is implemented by domain structures that embed Document.
type DocumentHolder interface {
	GetDocument() *Document
}

// DocumentUpdateFunc is a callback function passed to Document.Update.
// It receives a cloned, type-safe representation of the document's protobuf message,
// modifies it in-place, and returns any validation or application-level errors.
type DocumentUpdateFunc func(proto.Message) error

// Document provides optimistic-concurrency protobuf document persistence on top of StorageClient.
type Document struct {
	Client               DocumentClient
	Storage              storage.StorageClient
	BucketName           string
	ObjectName           string
	Revision             string
	LastRead             time.Time
	Rwlock               sync.RWMutex
	ProtoMsg             proto.Message
	SupportedSchemaMinor int32
}

// GetDocument returns the pointer to the underlying Document.
func (d *Document) GetDocument() *Document {
	return d
}

// UpdateFromStorage reloads the document from storage under a write lock.
func (d *Document) UpdateFromStorage() error {
	d.Rwlock.Lock()
	defer d.Rwlock.Unlock()

	return d.DoLoad()
}

// CheckAndReload queries the storage client for the object's current revision.
// If the revision matches d.Revision, it updates d.LastRead and returns (false, nil).
// If the revision has changed, it reloads the document from storage under a write lock
// and returns (true, nil).
func (d *Document) CheckAndReload(ctx context.Context) (bool, error) {
	d.Rwlock.RLock()
	storageClient := d.Storage
	object := d.ObjectName
	localRev := d.Revision
	d.Rwlock.RUnlock()

	if storageClient == nil {
		return false, errors.New("storage client cannot be nil")
	}

	remoteRev, err := storageClient.GetCurrentRevision(ctx, object)
	if err != nil {
		return false, fmt.Errorf("failed to get current revision for %s: %w", object, err)
	}

	if remoteRev == localRev {
		d.Rwlock.Lock()
		d.LastRead = time.Now()
		d.Rwlock.Unlock()
		return false, nil
	}

	// Revision changed; reload under write lock.
	d.Rwlock.Lock()
	defer d.Rwlock.Unlock()

	// Double check under write lock to avoid duplicate reload if another goroutine reloaded it.
	if remoteRev != "" && d.Revision == remoteRev {
		return false, nil
	}

	if err := d.DoLoad(); err != nil {
		return false, fmt.Errorf("failed to reload document %s: %w", object, err)
	}

	return true, nil
}

func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if msg == "storage: object doesn't exist" || strings.Contains(msg, "doesn't exist") || strings.Contains(strings.ToLower(msg), "not found") {
		return true
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		if apiErr.ErrorCode() == "NoSuchKey" || apiErr.ErrorCode() == "NotFound" {
			return true
		}
	}
	return false
}

// DoLoad fetches the document from storage, handling missing documents as empty initial state.
func (d *Document) DoLoad() error {
	data, revision, err := d.Storage.ReadObject(context.Background(), d.ObjectName)
	if err != nil {
		if isNotFoundError(err) {
			if d.ProtoMsg != nil {
				proto.Reset(d.ProtoMsg)
			}
			if d.Client != nil {
				err = d.Client.ReadFromProto(nil)
				if err != nil {
					return err
				}
			}
			d.Revision = ""
			d.LastRead = time.Now()
			return nil
		}
		return err
	}

	if d.ProtoMsg != nil {
		proto.Reset(d.ProtoMsg)
	}
	if d.Client != nil {
		err = d.Client.ReadFromProto(data)
		if err != nil {
			return err
		}
	}
	d.Revision = revision
	d.LastRead = time.Now()
	return nil
}

// Update performs an optimistic concurrency update loop: loads, validates schema, clones, applies updateFn, stamps version, and writes conditionally.
func (d *Document) Update(ctx context.Context, updateFn DocumentUpdateFunc) error {
	d.Rwlock.Lock()
	defer d.Rwlock.Unlock()

	for i := 0; i < 100; i++ {
		if err := d.DoLoad(); err != nil {
			return err
		}

		if d.Client != nil && d.Client.GetSchemaMinorVersion() > d.SupportedSchemaMinor {
			return ErrNewerSchemaVersionWriteForbidden
		}

		var cloned proto.Message
		if d.ProtoMsg != nil {
			cloned = proto.Clone(d.ProtoMsg)
		}

		if err := updateFn(cloned); err != nil {
			return err
		}

		if d.Client != nil {
			d.Client.StampVersion(cloned, d.SupportedSchemaMinor, version.GetGitCommit())
		}

		var newProtobuf []byte
		var err error
		if cloned != nil {
			newProtobuf, err = proto.Marshal(cloned)
			if err != nil {
				return err
			}
		}

		newRev, err := d.Storage.WriteObjectIfRevisionMatch(ctx, d.ObjectName, newProtobuf, d.Revision)
		if err != nil {
			if errors.Is(err, storage.RevisionWriteError) || err == storage.RevisionWriteError {
				continue
			}
			return err
		}

		// Succeeded
		d.Revision = newRev
		d.LastRead = time.Now()

		if d.ProtoMsg != nil {
			proto.Reset(d.ProtoMsg)
		}
		if d.Client != nil {
			err = d.Client.ReadFromProto(newProtobuf)
			if err != nil {
				return err
			}
		}

		return nil
	}

	return storage.WriteFailedError
}
