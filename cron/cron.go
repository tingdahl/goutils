package cron

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/tingdahl/goutils/httputil"
)

// Task is a maintenance function that can be executed during a cron run.
type Task func(ctx context.Context) error

// Registry maintains a thread-safe registry of named maintenance tasks.
type Registry struct {
	mu    sync.RWMutex
	tasks map[string]Task
}

var defaultRegistry = NewRegistry()

// NewRegistry creates a new independent Task Registry.
func NewRegistry() *Registry {
	return &Registry{
		tasks: make(map[string]Task),
	}
}

// Register adds a named task to the registry.
func (r *Registry) Register(name string, task Task) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tasks[name] = task
}

// RegisterTask registers a named task with the default package-level registry.
func RegisterTask(name string, task Task) {
	defaultRegistry.Register(name, task)
}

// GetDefaultRegistry returns the package-level default registry.
func GetDefaultRegistry() *Registry {
	return defaultRegistry
}

// Handler creates an http.Handler that validates the cron secret and runs all registered tasks.
func (r *Registry) Handler(secretResolver func() string, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}

	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()

		var expectedSecret string
		if secretResolver != nil {
			expectedSecret = secretResolver()
		}

		if expectedSecret == "" {
			logger.ErrorContext(ctx, "Cron invocation rejected: CRON_SECRET is not configured")
			httputil.WriteJSONError(w, "Unauthorized: Cron secret not configured", http.StatusUnauthorized)
			return
		}

		provided := ""
		authHeader := req.Header.Get(httputil.HeaderAuthorization)
		if strings.HasPrefix(authHeader, httputil.BearerPrefix) {
			provided = strings.TrimPrefix(authHeader, httputil.BearerPrefix)
		} else if cronKey := req.Header.Get("X-Cron-Key"); cronKey != "" {
			provided = cronKey
		}

		if subtle.ConstantTimeCompare([]byte(provided), []byte(expectedSecret)) != 1 {
			logger.WarnContext(ctx, "Unauthorized cron invocation attempt")
			httputil.WriteJSONError(w, httputil.MsgUnauthorized, http.StatusUnauthorized)
			return
		}

		logger.InfoContext(ctx, "Starting cron maintenance execution")

		// Snapshot tasks to execute under read lock
		r.mu.RLock()
		taskNames := make([]string, 0, len(r.tasks))
		taskMap := make(map[string]Task, len(r.tasks))
		for k, v := range r.tasks {
			taskNames = append(taskNames, k)
			taskMap[k] = v
		}
		r.mu.RUnlock()

		sort.Strings(taskNames)

		var failedTasks []string
		taskErrors := make(map[string]string)

		for _, name := range taskNames {
			fn := taskMap[name]
			if fn == nil {
				continue
			}
			if err := fn(ctx); err != nil {
				logger.ErrorContext(ctx, "Cron maintenance task failed", "task", name, "error", err)
				failedTasks = append(failedTasks, name)
				taskErrors[name] = err.Error()
			} else {
				logger.InfoContext(ctx, "Cron maintenance task completed successfully", "task", name)
			}
		}

		if len(failedTasks) > 0 {
			w.Header().Set(httputil.HeaderContentType, httputil.ContentTypeJSONUTF8)
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "error",
				"errors": taskErrors,
			})
			return
		}

		w.Header().Set(httputil.HeaderContentType, httputil.ContentTypeJSONUTF8)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":         "ok",
			"tasks_executed": taskNames,
		})
	})
}

// Handler returns an http.Handler using the default package-level registry.
func Handler(secretResolver func() string, logger *slog.Logger) http.Handler {
	return defaultRegistry.Handler(secretResolver, logger)
}
