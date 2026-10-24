package audit

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tingdahl/goutils/storage"
	"github.com/tingdahl/goutils/telemetry"
)

func TestAuditLogSingleAndQuery(t *testing.T) {
	mockStore := storage.NewMockStorageClient()

	loc, err := time.LoadLocation("Europe/Stockholm")
	if err != nil {
		t.Fatalf("Failed to load timezone: %v", err)
	}

	ctx := context.Background()
	ctx = telemetry.WithClientIP(ctx, "192.168.1.50")
	ctx = telemetry.WithUserAgent(ctx, "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)")
	ctx = telemetry.WithUserMetadata(ctx, "user-123", "alice@example.com", "Alice Lindgren", "https://accounts.google.com", "sub-99887766")

	prefix := "companies/1001/audit/"
	err = Log(ctx, mockStore, prefix, AuditEntry{
		Module:       "ACCOUNTING",
		Action:       "TRANSACTION_UPDATE",
		HumanSummary: "Updated transaction #20260001",
		TechnicalDetails: map[string]string{
			"tx_id": "20260001",
		},
	}, loc)
	if err != nil {
		t.Fatalf("Log failed: %v", err)
	}

	entries, err := Query(ctx, mockStore, prefix, 100)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("Expected 1 audit entry, got %d", len(entries))
	}

	entry := entries[0]
	if entry.UserID != "user-123" || entry.UserEmail != "alice@example.com" || entry.UserName != "Alice Lindgren" || entry.OIDCSub != "sub-99887766" {
		t.Errorf("Unexpected user details: %+v", entry)
	}
	if entry.OIDCIssuer != "https://accounts.google.com" {
		t.Errorf("Expected OIDCIssuer https://accounts.google.com, got %s", entry.OIDCIssuer)
	}
	if entry.IPAddress != "192.168.1.50" {
		t.Errorf("Expected IP 192.168.1.50, got %s", entry.IPAddress)
	}
	if entry.Module != "ACCOUNTING" {
		t.Errorf("Expected module ACCOUNTING, got %s", entry.Module)
	}
	if entry.TechnicalDetails["tx_id"] != "20260001" {
		t.Errorf("Expected technical_details tx_id=20260001, got %v", entry.TechnicalDetails)
	}
	if !strings.Contains(entry.TimestampFormatted, "CET") && !strings.Contains(entry.TimestampFormatted, "CEST") {
		t.Errorf("Expected CET/CEST in formatted timestamp, got %s", entry.TimestampFormatted)
	}
}

func TestAuditLogConcurrency(t *testing.T) {
	mockStore := storage.NewMockStorageClient()

	prefix := "companies/2002/audit/"
	const numWriters = 20
	const writesPerRoutine = 5

	var wg sync.WaitGroup
	wg.Add(numWriters)

	for i := 0; i < numWriters; i++ {
		go func(writerID int) {
			defer wg.Done()
			for j := 0; j < writesPerRoutine; j++ {
				ctx := context.Background()
				ctx = telemetry.WithClientIP(ctx, fmt.Sprintf("10.0.0.%d", writerID))
				ctx = telemetry.WithUserMetadata(ctx, fmt.Sprintf("user-%d", writerID), fmt.Sprintf("user%d@example.com", writerID), "", "", "")

				err := Log(ctx, mockStore, prefix, AuditEntry{
					Module:       "BANKING",
					Action:       "TRANSACTION_RECONCILE",
					HumanSummary: fmt.Sprintf("Writer %d write %d reconciled transaction", writerID, j),
				})
				if err != nil {
					t.Errorf("Concurrent Log error: %v", err)
				}
			}
		}(i)
	}

	wg.Wait()

	entries, err := Query(context.Background(), mockStore, prefix, 200)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	expectedTotal := numWriters * writesPerRoutine
	if len(entries) != expectedTotal {
		t.Fatalf("Expected %d audit entries, got %d", expectedTotal, len(entries))
	}
}
