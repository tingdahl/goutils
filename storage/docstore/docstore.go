package docstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tingdahl/goutils/storage"
	"github.com/tingdahl/goutils/version"

	"github.com/aws/smithy-go"
	"golang.org/x/sync/singleflight"
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

// RWMutex wraps sync.RWMutex and automatically enforces the owning Document's
// RevisionPolicy before acquiring a read lock.
type RWMutex struct {
	mu  sync.RWMutex
	doc *Document
}

// RLock checks whether the owning Document needs a storage revision check according
// to its configured RevisionPolicy (reloading if the remote revision changed) before
// acquiring the read lock.
func (m *RWMutex) RLock() {
	if m.doc != nil {
		m.doc.maybeRefreshBeforeRead()
	}
	m.mu.RLock()
}

// TryRLock attempts to acquire the read lock without blocking on an active writer.
// If the lock is free and the document is stale per its RevisionPolicy, it refreshes
// before acquiring the read lock.
func (m *RWMutex) TryRLock() bool {
	if m.doc != nil && m.doc.needsRevisionCheck(getOpts{}) {
		if !m.mu.TryRLock() {
			return false
		}
		m.mu.RUnlock()
		m.doc.maybeRefreshBeforeRead()
	}
	return m.mu.TryRLock()
}

// RUnlock releases a read lock.
func (m *RWMutex) RUnlock() {
	m.mu.RUnlock()
}

// Lock acquires a write lock on the document.
func (m *RWMutex) Lock() {
	m.mu.Lock()
}

// TryLock attempts to acquire a write lock without blocking.
func (m *RWMutex) TryLock() bool {
	return m.mu.TryLock()
}

// Unlock releases the write lock and marks the document as freshly checked.
func (m *RWMutex) Unlock() {
	if m.doc != nil {
		m.doc.lastCheckedNano.Store(time.Now().UnixNano())
	}
	m.mu.Unlock()
}

// Document provides optimistic-concurrency protobuf document persistence on top of StorageClient.
type Document struct {
	Client               DocumentClient
	Storage              storage.StorageClient
	BucketName           string
	ObjectName           string
	Revision             string
	LastRead             time.Time
	Rwlock               RWMutex
	ProtoMsg             proto.Message
	SupportedSchemaMinor int32

	policyMode         atomic.Int32
	policyIntervalNano atomic.Int64
	lastCheckedNano    atomic.Int64
	checkFlight        singleflight.Group
}

// GetDocument returns the pointer to the underlying Document and ensures Rwlock is bound.
func (d *Document) GetDocument() *Document {
	if d != nil && d.Rwlock.doc == nil {
		d.Rwlock.mu.Lock()
		d.Rwlock.doc = d
		d.Rwlock.mu.Unlock()
	}
	return d
}

// SetRevisionPolicy configures the revision checking policy for this document
// and binds Rwlock to this document instance.
func (d *Document) SetRevisionPolicy(policy RevisionPolicy) {
	if d == nil {
		return
	}
	d.Rwlock.mu.Lock()
	d.Rwlock.doc = d
	if d.lastCheckedNano.Load() == 0 {
		if !d.LastRead.IsZero() {
			d.lastCheckedNano.Store(d.LastRead.UnixNano())
		} else {
			now := time.Now()
			d.LastRead = now
			d.lastCheckedNano.Store(now.UnixNano())
		}
	}
	d.Rwlock.mu.Unlock()
	d.policyMode.Store(int32(policy.mode))
	d.policyIntervalNano.Store(int64(policy.interval))
}

func (d *Document) needsRevisionCheck(callOpts getOpts) bool {
	if d == nil {
		return false
	}
	if callOpts.forceCheck {
		return true
	}
	if callOpts.skipCheck {
		return false
	}
	lastNano := d.lastCheckedNano.Load()
	if callOpts.maxAge != nil {
		if lastNano == 0 {
			return true
		}
		return time.Since(time.Unix(0, lastNano)) >= *callOpts.maxAge
	}
	switch policyMode(d.policyMode.Load()) {
	case policyAlways:
		return true
	case policyInterval:
		if lastNano == 0 {
			return true
		}
		interval := time.Duration(d.policyIntervalNano.Load())
		return time.Since(time.Unix(0, lastNano)) >= interval
	case policyNever:
		return false
	default:
		return false
	}
}

func (d *Document) ensureFresh(ctx context.Context, callOpts getOpts) error {
	if !d.needsRevisionCheck(callOpts) {
		return nil
	}
	_, err, _ := d.checkFlight.Do("check", func() (any, error) {
		if !callOpts.forceCheck && !d.needsRevisionCheck(callOpts) {
			return nil, nil
		}
		if _, err := d.CheckAndReload(ctx); err != nil {
			return nil, err
		}
		return nil, nil
	})
	return err
}

func (d *Document) maybeRefreshBeforeRead() {
	if !d.needsRevisionCheck(getOpts{}) {
		return
	}
	if err := d.ensureFresh(context.Background(), getOpts{}); err != nil {
		// Back off until the next interval on transient storage errors so RLock()
		// does not hammer storage on every read during an outage.
		d.lastCheckedNano.Store(time.Now().UnixNano())
	}
}

// UpdateFromStorage reloads the document from storage under a write lock.
func (d *Document) UpdateFromStorage() error {
	d.Rwlock.mu.Lock()
	defer d.Rwlock.mu.Unlock()

	return d.DoLoad()
}

// CheckAndReload queries the storage client for the object's current revision.
// If the revision matches d.Revision, it updates d.LastRead and returns (false, nil).
// If the revision has changed, it reloads the document from storage under a write lock
// and returns (true, nil).
func (d *Document) CheckAndReload(ctx context.Context) (bool, error) {
	d.Rwlock.mu.RLock()
	storageClient := d.Storage
	object := d.ObjectName
	localRev := d.Revision
	d.Rwlock.mu.RUnlock()

	if storageClient == nil {
		return false, errors.New("storage client cannot be nil")
	}

	remoteRev, err := storageClient.GetCurrentRevision(ctx, object)
	if err != nil {
		return false, fmt.Errorf("failed to get current revision for %s: %w", object, err)
	}

	if remoteRev == localRev {
		now := time.Now()
		d.Rwlock.mu.Lock()
		d.Rwlock.doc = d
		d.LastRead = now
		d.lastCheckedNano.Store(now.UnixNano())
		d.Rwlock.mu.Unlock()
		return false, nil
	}

	// Revision changed; reload under write lock.
	d.Rwlock.mu.Lock()
	defer d.Rwlock.mu.Unlock()

	// Double check under write lock to avoid duplicate reload if another goroutine reloaded it.
	if remoteRev != "" && d.Revision == remoteRev {
		now := time.Now()
		d.Rwlock.doc = d
		d.LastRead = now
		d.lastCheckedNano.Store(now.UnixNano())
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
	d.Rwlock.doc = d
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
			now := time.Now()
			d.Revision = ""
			d.LastRead = now
			d.lastCheckedNano.Store(now.UnixNano())
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
	now := time.Now()
	d.Revision = revision
	d.LastRead = now
	d.lastCheckedNano.Store(now.UnixNano())
	return nil
}

// Update performs an optimistic concurrency update loop: loads, validates schema, clones, applies updateFn, stamps version, and writes conditionally.
func (d *Document) Update(ctx context.Context, updateFn DocumentUpdateFunc) error {
	d.Rwlock.mu.Lock()
	defer d.Rwlock.mu.Unlock()
	d.Rwlock.doc = d

	for i := 0; i < 100; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}

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
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, storage.RevisionWriteError) || err == storage.RevisionWriteError {
				continue
			}
			return err
		}

		// Succeeded
		now := time.Now()
		d.Revision = newRev
		d.LastRead = now
		d.lastCheckedNano.Store(now.UnixNano())

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
