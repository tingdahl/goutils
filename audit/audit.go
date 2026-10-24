package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tingdahl/goutils/storage"
	"github.com/tingdahl/goutils/telemetry"
)

type AuditEntry struct {
	ID                 string            `json:"id"`
	TimestampUnixMs    int64             `json:"timestamp_unix_ms"`
	TimestampFormatted string            `json:"timestamp_formatted"`
	UserID             string            `json:"user_id,omitempty"`
	UserEmail          string            `json:"user_email,omitempty"`
	UserName           string            `json:"user_name,omitempty"`
	OIDCIssuer         string            `json:"oidc_issuer,omitempty"`
	OIDCSub            string            `json:"oidc_sub,omitempty"`
	IPAddress          string            `json:"ip_address,omitempty"`
	UserAgent          string            `json:"user_agent,omitempty"`
	TraceID            string            `json:"trace_id,omitempty"`
	SessionID          string            `json:"session_id,omitempty"`
	Module             string            `json:"module"`
	Action             string            `json:"action"`
	HumanSummary       string            `json:"human_summary"`
	TechnicalDetails   map[string]string `json:"technical_details,omitempty"`
}

// FormatTimestamp formats a time using RFC3339 or a specified location if provided.
func FormatTimestamp(t time.Time, loc ...*time.Location) string {
	if len(loc) > 0 && loc[0] != nil {
		return t.In(loc[0]).Format("2006-01-02 15:04:05 MST")
	}
	return t.Format(time.RFC3339)
}

// ObjectKey returns the standard object key for an audit entry under a prefix: "<prefix>/<timestampMs>_<id>.json".
func ObjectKey(prefix string, timestampMs int64, id string) string {
	cleanPrefix := strings.TrimSuffix(prefix, "/")
	if cleanPrefix == "" {
		return fmt.Sprintf("%d_%s.json", timestampMs, id)
	}
	return fmt.Sprintf("%s/%d_%s.json", cleanPrefix, timestampMs, id)
}

// Log records an audit entry to object storage under the specified prefix.
// If sc is nil, it uses storage.NewStorageClient().
// Enriches unpopulated fields (UserID, UserEmail, ClientIP, etc.) from ctx via telemetry.
func Log(ctx context.Context, sc storage.StorageClient, prefix string, entry AuditEntry, loc ...*time.Location) error {
	if strings.TrimSpace(prefix) == "" {
		return fmt.Errorf("audit: prefix is required")
	}

	now := time.Now()
	if entry.TimestampUnixMs == 0 {
		entry.TimestampUnixMs = now.UnixMilli()
	}
	if entry.ID == "" {
		entry.ID = uuid.New().String()
	}
	if entry.TimestampFormatted == "" {
		entry.TimestampFormatted = FormatTimestamp(time.UnixMilli(entry.TimestampUnixMs), loc...)
	}

	// Enrich from User in context if not explicitly set
	if entry.UserID == "" {
		entry.UserID = telemetry.GetUserIDFromContext(ctx)
	}
	if entry.UserEmail == "" {
		entry.UserEmail = telemetry.GetEmailFromContext(ctx)
	}
	if entry.UserName == "" {
		entry.UserName = telemetry.GetUserNameFromContext(ctx)
	}
	if entry.OIDCIssuer == "" {
		entry.OIDCIssuer = telemetry.GetOIDCIssuerFromContext(ctx)
	}
	if entry.OIDCSub == "" {
		entry.OIDCSub = telemetry.GetOIDCSubFromContext(ctx)
	}

	// Enrich network & request metadata from context if not explicitly set
	if entry.IPAddress == "" {
		entry.IPAddress = telemetry.GetClientIPFromContext(ctx)
	}
	if entry.UserAgent == "" {
		entry.UserAgent = telemetry.GetUserAgentFromContext(ctx)
	}
	if entry.TraceID == "" {
		entry.TraceID = telemetry.GetTraceID(ctx)
	}
	if entry.SessionID == "" {
		entry.SessionID = telemetry.GetSessionID(ctx)
	}

	data, err := json.Marshal(entry)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to marshal audit entry", "error", err, "prefix", prefix)
		return err
	}

	if sc == nil {
		client, err := storage.NewStorageClient()
		if err != nil {
			slog.ErrorContext(ctx, "Failed to create storage client for audit log", "error", err, "prefix", prefix)
			return err
		}
		sc = client
	}

	key := ObjectKey(prefix, entry.TimestampUnixMs, entry.ID)
	_, err = sc.WriteObject(ctx, key, data)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to write audit log object", "key", key, "error", err, "prefix", prefix)
		return err
	}

	return nil
}

// Query reads and returns audit log entries under the specified prefix ordered chronologically.
// Concurrently fetches entries using a bounded worker pool.
func Query(ctx context.Context, sc storage.StorageClient, prefix string, limit int) ([]AuditEntry, error) {
	if sc == nil {
		client, err := storage.NewStorageClient()
		if err != nil {
			return nil, fmt.Errorf("audit: failed to create storage client: %w", err)
		}
		sc = client
	}

	cleanPrefix := prefix
	if !strings.HasSuffix(cleanPrefix, "/") {
		cleanPrefix += "/"
	}

	objects, err := sc.ListObjects(ctx, cleanPrefix)
	if err != nil {
		return nil, fmt.Errorf("audit: failed to list audit objects: %w", err)
	}

	if len(objects) == 0 {
		return []AuditEntry{}, nil
	}

	entries := make([]AuditEntry, 0, len(objects))
	var mu sync.Mutex
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 16) // bounded concurrency

	for _, obj := range objects {
		if !strings.HasSuffix(obj.Key, ".json") {
			continue
		}
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			data, _, err := sc.ReadObject(ctx, key)
			if err != nil {
				slog.WarnContext(ctx, "Failed to read audit object", "key", key, "error", err)
				return
			}

			var item AuditEntry
			if err := json.Unmarshal(data, &item); err != nil {
				slog.WarnContext(ctx, "Failed to unmarshal audit object", "key", key, "error", err)
				return
			}

			mu.Lock()
			entries = append(entries, item)
			mu.Unlock()
		}(obj.Key)
	}

	wg.Wait()

	// Sort chronologically ascending (oldest first)
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].TimestampUnixMs == entries[j].TimestampUnixMs {
			return entries[i].ID < entries[j].ID
		}
		return entries[i].TimestampUnixMs < entries[j].TimestampUnixMs
	})

	if limit > 0 && len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}

	return entries, nil
}
