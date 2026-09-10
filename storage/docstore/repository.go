package docstore

import (
	"context"
	"errors"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// DefaultRevisionCheckInterval is the standard revision check interval (5s) for multi-pod environments.
const DefaultRevisionCheckInterval = 5 * time.Second

// Factory is called to instantiate and load a document when a cache miss occurs.
type Factory[T DocumentHolder] func(ctx context.Context, objectPath string) (T, error)

type policyMode int

const (
	policyNever policyMode = iota
	policyAlways
	policyInterval
)

// RevisionPolicy defines the strategy for checking if an in-memory document matches storage revision.
type RevisionPolicy struct {
	mode     policyMode
	interval time.Duration
}

var (
	// CheckNever never checks the storage revision automatically.
	CheckNever = RevisionPolicy{mode: policyNever}

	// CheckAlways checks the storage revision on every Get() call.
	CheckAlways = RevisionPolicy{mode: policyAlways}
)

// CheckInterval checks storage revision only if at least interval has passed since the last check.
func CheckInterval(interval time.Duration) RevisionPolicy {
	return RevisionPolicy{
		mode:     policyInterval,
		interval: interval,
	}
}

// DefaultRevisionPolicy returns the default revision checking policy (5s check interval).
func DefaultRevisionPolicy() RevisionPolicy {
	return CheckInterval(DefaultRevisionCheckInterval)
}

type repoConfig struct {
	policy      RevisionPolicy
	maxLifetime time.Duration
}

// RepositoryOption configures a Repository instance.
type RepositoryOption func(*repoConfig)

// WithRevisionPolicy sets the default revision checking policy.
func WithRevisionPolicy(p RevisionPolicy) RepositoryOption {
	return func(c *repoConfig) {
		c.policy = p
	}
}

// WithMaxLifetime sets the idle duration threshold after which entries are pruned during Get().
// A value of 0 (default) disables lifetime pruning.
func WithMaxLifetime(d time.Duration) RepositoryOption {
	return func(c *repoConfig) {
		c.maxLifetime = d
	}
}

type getOpts struct {
	forceCheck bool
	skipCheck  bool
	maxAge     *time.Duration
}

// GetOption configures per-call behavior for Repository.Get.
type GetOption func(*getOpts)

// WithForceCheck forces a storage revision check regardless of configured policy.
func WithForceCheck() GetOption {
	return func(o *getOpts) {
		o.forceCheck = true
	}
}

// WithSkipCheck skips any storage revision check regardless of configured policy.
func WithSkipCheck() GetOption {
	return func(o *getOpts) {
		o.skipCheck = true
	}
}

// WithMaxAge overrides the revision check interval for this specific Get call.
func WithMaxAge(d time.Duration) GetOption {
	return func(o *getOpts) {
		o.maxAge = &d
	}
}

type cacheEntry[T DocumentHolder] struct {
	doc          T
	lastChecked  time.Time
	lastAccessed time.Time
}

// Repository manages in-memory caching, staleness checks, and lifetime pruning for documents of type T.
type Repository[T DocumentHolder] struct {
	mu          sync.RWMutex
	entries     map[string]*cacheEntry[T]
	factory     Factory[T]
	policy      RevisionPolicy
	maxLifetime time.Duration
	flight      singleflight.Group
}

// NewRepository creates a new generic document repository.
func NewRepository[T DocumentHolder](factory Factory[T], opts ...RepositoryOption) *Repository[T] {
	cfg := repoConfig{
		policy:      DefaultRevisionPolicy(),
		maxLifetime: 0,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	return &Repository[T]{
		entries:     make(map[string]*cacheEntry[T]),
		factory:     factory,
		policy:      cfg.policy,
		maxLifetime: cfg.maxLifetime,
	}
}

// Get retrieves a document from the repository by objectPath following a 3-step lifecycle:
// 1. Presence check: Return from memory if present (regardless of lifetime); load via factory if absent.
// 2. Generation check: Validate revision with storage if the policy or options dictate.
// 3. Lifetime pruning: If max lifetime is configured, prune entries that have exceeded the lifetime.
func (r *Repository[T]) Get(ctx context.Context, objectPath string, opts ...GetOption) (T, error) {
	var callOpts getOpts
	for _, opt := range opts {
		opt(&callOpts)
	}

	// 1. Presence Check: check if it's in memory
	r.mu.RLock()
	entry, exists := r.entries[objectPath]
	r.mu.RUnlock()

	var doc T
	justLoaded := false

	if exists {
		r.mu.Lock()
		entry.lastAccessed = time.Now()
		doc = entry.doc
		r.mu.Unlock()
	} else {
		// Cache miss: load via factory using singleflight to coalesce concurrent requests
		val, err, _ := r.flight.Do("load:"+objectPath, func() (any, error) {
			// Double check under lock
			r.mu.RLock()
			if e, ok := r.entries[objectPath]; ok {
				r.mu.RUnlock()
				return e.doc, nil
			}
			r.mu.RUnlock()

			if r.factory == nil {
				var zero T
				return zero, errors.New("document factory is nil")
			}

			loaded, err := r.factory(ctx, objectPath)
			if err != nil {
				var zero T
				return zero, err
			}

			now := time.Now()
			r.mu.Lock()
			r.entries[objectPath] = &cacheEntry[T]{
				doc:          loaded,
				lastChecked:  now,
				lastAccessed: now,
			}
			r.mu.Unlock()

			return loaded, nil
		})
		if err != nil {
			var zero T
			return zero, err
		}
		doc = val.(T)
		justLoaded = true
	}

	// 2. Generation / Revision Validation
	if !justLoaded {
		needsCheck := false
		if callOpts.forceCheck {
			needsCheck = true
		} else if callOpts.skipCheck {
			needsCheck = false
		} else if callOpts.maxAge != nil {
			r.mu.RLock()
			needsCheck = time.Since(entry.lastChecked) >= *callOpts.maxAge
			r.mu.RUnlock()
		} else {
			switch r.policy.mode {
			case policyAlways:
				needsCheck = true
			case policyInterval:
				r.mu.RLock()
				needsCheck = time.Since(entry.lastChecked) >= r.policy.interval
				r.mu.RUnlock()
			case policyNever:
				needsCheck = false
			}
		}

		if needsCheck {
			_, err, _ := r.flight.Do("check:"+objectPath, func() (any, error) {
				d := doc.GetDocument()
				if d == nil {
					return nil, errors.New("underlying document is nil")
				}

				if _, err := d.CheckAndReload(ctx); err != nil {
					return nil, err
				}

				r.mu.Lock()
				if e, ok := r.entries[objectPath]; ok {
					e.lastChecked = time.Now()
				}
				r.mu.Unlock()

				return nil, nil
			})
			if err != nil {
				var zero T
				return zero, err
			}
		}
	}

	// 3. Lifetime Pruning: loop through repository and throw out entries older than maxLifetime
	if r.maxLifetime > 0 {
		now := time.Now()
		r.mu.Lock()
		for p, e := range r.entries {
			if p == objectPath {
				continue
			}
			if now.Sub(e.lastAccessed) > r.maxLifetime {
				delete(r.entries, p)
			}
		}
		r.mu.Unlock()
	}

	return doc, nil
}
