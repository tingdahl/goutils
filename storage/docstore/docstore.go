package docstore

import (
	"context"
	"errors"
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

// UpdateFromStorage reloads the document from storage under a write lock.
func (d *Document) UpdateFromStorage() error {
	d.Rwlock.Lock()
	defer d.Rwlock.Unlock()

	return d.DoLoad()
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
	data, revision, err := d.Storage.ReadObject(context.Background(), d.BucketName, d.ObjectName)
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

		_, err = d.Storage.WriteObjectIfRevisionMatch(ctx, d.BucketName, d.ObjectName, newProtobuf, d.Revision)
		if err != nil {
			if errors.Is(err, storage.RevisionWriteError) || err == storage.RevisionWriteError {
				continue
			}
			return err
		}

		// Succeeded
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
