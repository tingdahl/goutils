package email_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/tingdahl/goutils/email"
	"github.com/tingdahl/goutils/storage"
	"google.golang.org/protobuf/proto"
)

var (
	historyTestStore = storage.NewMockStorageClient()
	historyPrefix    = "tenants"
)

func initHistoryTestPackage(t *testing.T) {
	t.Helper()
	email.ResetForTesting()
	err := email.Init(historyTestStore, historyPrefix)
	if err != nil {
		t.Fatalf("email.Init failed: %v", err)
	}
}

func TestHistory_InitValidation(t *testing.T) {
	email.ResetForTesting()
	if err := email.Init(nil, "prefix"); err == nil {
		t.Errorf("expected error with nil store, got nil")
	}
}

func TestHistory_GetClientValidation(t *testing.T) {
	email.ResetForTesting()
	ctx := context.Background()

	// Before Init
	if _, err := email.GetHistoryClient(ctx, 1); err == nil {
		t.Errorf("expected error when uninitialized, got nil")
	}

	initHistoryTestPackage(t)

	// Tenant ID <= 0
	if _, err := email.GetHistoryClient(ctx, 0); err == nil {
		t.Errorf("expected error for tenant ID 0, got nil")
	}
	if _, err := email.GetHistoryClient(ctx, -10); err == nil {
		t.Errorf("expected error for negative tenant ID, got nil")
	}
}

func TestHistory_TenantPath(t *testing.T) {
	initHistoryTestPackage(t)
	path := email.TenantEmailHistoryPath(123)
	expected := "tenants/123/email_history.v1.pb.zst"
	if path != expected {
		t.Errorf("expected path %q, got %q", expected, path)
	}
}

func TestHistory_SendTemplate_Success(t *testing.T) {
	initHistoryTestPackage(t)
	ctx := context.Background()

	mockSender := email.NewMockSender()
	email.SetDefaultSender(mockSender)

	// Register template with default From address
	err := email.RegisterTemplate(email.TemplateDef{
		ID:      "sub_expiring_30d",
		From:    email.NewAddress("noreply@example.com", "System"),
		Subject: "Subscription ending in {{.Days}} days",
		HTML:    "<p>Hello {{.Name}}, subscription ends soon.</p>",
		Text:    "Hello {{.Name}}, subscription ends soon.",
	})
	if err != nil {
		t.Fatalf("RegisterTemplate failed: %v", err)
	}

	const tenantID int64 = 42
	client, err := email.GetHistoryClient(ctx, tenantID)
	if err != nil {
		t.Fatalf("GetHistoryClient failed: %v", err)
	}

	req := email.SendTemplateRequest{
		TemplateID:       "sub_expiring_30d",
		Recipient:        email.NewAddress("alice@customer.com", "Alice"),
		DeduplicationKey: "sub_expiring_30d:sub_123:2026-11-30",
		Data: map[string]any{
			"Name": "Alice",
			"Days": 30,
		},
	}

	rec, err := client.SendTemplate(ctx, req)
	if err != nil {
		t.Fatalf("SendTemplate failed: %v", err)
	}

	if rec == nil {
		t.Fatal("expected non-nil record")
	}
	if rec.Id <= 0 {
		t.Errorf("expected positive int64 ID, got %d", rec.Id)
	}
	if rec.Status != email.EmailStatus_EMAIL_STATUS_SENT {
		t.Errorf("expected status SENT, got %v", rec.Status)
	}
	if rec.Subject != "Subscription ending in 30 days" {
		t.Errorf("expected rendered subject, got %q", rec.Subject)
	}
	if rec.RecipientEmail != "alice@customer.com" {
		t.Errorf("expected recipient alice@customer.com, got %q", rec.RecipientEmail)
	}
	if rec.SentAtUnixMs <= 0 {
		t.Errorf("expected positive SentAtUnixMs, got %d", rec.SentAtUnixMs)
	}

	// Verify template default From address was used
	sentMsgs := mockSender.SentMessages()
	if len(sentMsgs) != 1 {
		t.Fatalf("expected 1 sent message in mock, got %d", len(sentMsgs))
	}
	if sentMsgs[0].From.Email != "noreply@example.com" {
		t.Errorf("expected default From address, got %q", sentMsgs[0].From.Email)
	}

	// Verify request-level From override takes precedence over template From
	overrideFrom := email.NewAddress("billing-custom@example.com", "Billing Override")
	_, err = client.SendTemplate(ctx, email.SendTemplateRequest{
		TemplateID:       "sub_expiring_30d",
		From:             &overrideFrom,
		Recipient:        email.NewAddress("bob@customer.com", "Bob"),
		DeduplicationKey: "sub_expiring_30d:bob:override",
		Data: map[string]any{
			"Name": "Bob",
			"Days": 15,
		},
	})
	if err != nil {
		t.Fatalf("SendTemplate with override From failed: %v", err)
	}
	sentMsgs = mockSender.SentMessages()
	if len(sentMsgs) != 2 {
		t.Fatalf("expected 2 sent messages, got %d", len(sentMsgs))
	}
	if sentMsgs[1].From.Email != "billing-custom@example.com" {
		t.Errorf("expected override From address, got %q", sentMsgs[1].From.Email)
	}

	// Query from client
	fetched := client.Get(rec.Id)
	if fetched == nil || fetched.Id != rec.Id {
		t.Errorf("Get(id) failed to find record")
	}

	fetchedByDedup := client.GetByDeduplicationKey("sub_expiring_30d:sub_123:2026-11-30")
	if fetchedByDedup == nil || fetchedByDedup.Id != rec.Id {
		t.Errorf("GetByDeduplicationKey failed to find record")
	}
}

func TestHistory_SendTemplate_Deduplication(t *testing.T) {
	initHistoryTestPackage(t)
	ctx := context.Background()

	mockSender := email.NewMockSender()
	email.SetDefaultSender(mockSender)

	_ = email.RegisterTemplate(email.TemplateDef{
		ID:      "welcome_email",
		From:    email.NewAddress("noreply@example.com", "System"),
		Subject: "Welcome!",
		Text:    "Welcome to the platform.",
	})

	const tenantID int64 = 88
	client, err := email.GetHistoryClient(ctx, tenantID)
	if err != nil {
		t.Fatalf("GetHistoryClient failed: %v", err)
	}

	req := email.SendTemplateRequest{
		TemplateID:       "welcome_email",
		Recipient:        email.NewAddress("user@example.com", "User"),
		DeduplicationKey: "welcome:user@example.com",
	}

	// First send -> succeeds
	rec1, err := client.SendTemplate(ctx, req)
	if err != nil {
		t.Fatalf("first SendTemplate failed: %v", err)
	}
	if rec1.Status != email.EmailStatus_EMAIL_STATUS_SENT {
		t.Errorf("expected rec1 status SENT, got %v", rec1.Status)
	}

	// Second send with same deduplication key -> must return ErrAlreadySent
	rec2, err := client.SendTemplate(ctx, req)
	if !errors.Is(err, email.ErrAlreadySent) {
		t.Errorf("expected ErrAlreadySent on duplicate send, got %v", err)
	}
	if rec2 != nil {
		t.Errorf("expected nil record on duplicate send error, got %+v", rec2)
	}

	// Mock sender should have been called only once
	if len(mockSender.SentMessages()) != 1 {
		t.Errorf("expected exactly 1 mock send, got %d", len(mockSender.SentMessages()))
	}
}

func TestHistory_SendTemplate_FailedSendCanRetry(t *testing.T) {
	initHistoryTestPackage(t)
	ctx := context.Background()

	mockSender := email.NewMockSender()
	email.SetDefaultSender(mockSender)

	_ = email.RegisterTemplate(email.TemplateDef{
		ID:      "retry_test",
		From:    email.NewAddress("noreply@example.com", "System"),
		Subject: "Test",
		Text:    "Body",
	})

	const tenantID int64 = 99
	client, err := email.GetHistoryClient(ctx, tenantID)
	if err != nil {
		t.Fatalf("GetHistoryClient failed: %v", err)
	}

	dedupKey := "fail_and_retry:123"
	req := email.SendTemplateRequest{
		TemplateID:       "retry_test",
		Recipient:        email.NewAddress("fail@example.com", "Fail"),
		DeduplicationKey: dedupKey,
	}

	// Configure mock to fail
	mockSender.SetSendFunc(func(ctx context.Context, msg *email.Message) error {
		return errors.New("network timeout")
	})

	rec1, err := client.SendTemplate(ctx, req)
	if err == nil {
		t.Fatalf("expected send error, got nil")
	}
	if rec1 == nil || rec1.Status != email.EmailStatus_EMAIL_STATUS_FAILED {
		t.Errorf("expected record status FAILED, got %+v", rec1)
	}
	if rec1.LastError != "network timeout" {
		t.Errorf("expected LastError 'network timeout', got %q", rec1.LastError)
	}

	// Clear mock failure and retry
	mockSender.SetSendFunc(nil)
	rec2, err := client.SendTemplate(ctx, req)
	if err != nil {
		t.Fatalf("retry SendTemplate failed: %v", err)
	}
	if rec2.Status != email.EmailStatus_EMAIL_STATUS_SENT {
		t.Errorf("expected retried record status SENT, got %v", rec2.Status)
	}
	if rec2.Id != rec1.Id {
		t.Errorf("expected retry to reuse record ID %d, got %d", rec1.Id, rec2.Id)
	}
}

func TestHistory_ActiveClaimAndExpiredClaim(t *testing.T) {
	initHistoryTestPackage(t)
	ctx := context.Background()

	mockSender := email.NewMockSender()
	email.SetDefaultSender(mockSender)

	_ = email.RegisterTemplate(email.TemplateDef{
		ID:      "claim_test",
		From:    email.NewAddress("noreply@example.com", "System"),
		Subject: "Claim test",
		Text:    "Body",
	})

	const tenantID int64 = 105
	client, _ := email.GetHistoryClient(ctx, tenantID)

	dedupKey := "claim_test_key"

	// Artificially inject an active CLAIMED record
	nowMs := time.Now().UnixMilli()
	_ = client.Update(ctx, func(m proto.Message) error {
		doc := m.(*email.EmailHistoryProto)
		doc.Emails = append(doc.Emails, &email.EmailRecordProto{
			Id:               1001,
			DeduplicationKey: dedupKey,
			TemplateId:       "claim_test",
			RecipientEmail:   "user@example.com",
			Status:           email.EmailStatus_EMAIL_STATUS_CLAIMED,
			ClaimedAtUnixMs:  nowMs, // fresh claim
		})
		return nil
	})

	// Pod B attempts to send -> should receive ErrCurrentlyClaimed
	req := email.SendTemplateRequest{
		TemplateID:       "claim_test",
		Recipient:        email.NewAddress("user@example.com", "User"),
		DeduplicationKey: dedupKey,
		ClaimTimeout:     10 * time.Minute,
	}

	_, err := client.SendTemplate(ctx, req)
	if !errors.Is(err, email.ErrCurrentlyClaimed) {
		t.Errorf("expected ErrCurrentlyClaimed, got %v", err)
	}

	// Now artificially expire the claim (e.g. 20 minutes ago)
	_ = client.Update(ctx, func(m proto.Message) error {
		doc := m.(*email.EmailHistoryProto)
		for _, rec := range doc.Emails {
			if rec.DeduplicationKey == dedupKey {
				rec.ClaimedAtUnixMs = nowMs - (20 * time.Minute).Milliseconds()
			}
		}
		return nil
	})

	// Pod B attempts again -> should reclaim and send successfully
	rec, err := client.SendTemplate(ctx, req)
	if err != nil {
		t.Fatalf("reclaim SendTemplate failed: %v", err)
	}
	if rec.Status != email.EmailStatus_EMAIL_STATUS_SENT {
		t.Errorf("expected reclaimed status SENT, got %v", rec.Status)
	}
}

func TestHistory_ListAndFilters(t *testing.T) {
	initHistoryTestPackage(t)
	ctx := context.Background()

	mockSender := email.NewMockSender()
	email.SetDefaultSender(mockSender)

	_ = email.RegisterTemplate(email.TemplateDef{
		ID:      "type_a",
		From:    email.NewAddress("from@example.com", "From"),
		Subject: "A",
		Text:    "A",
	})
	_ = email.RegisterTemplate(email.TemplateDef{
		ID:      "type_b",
		From:    email.NewAddress("from@example.com", "From"),
		Subject: "B",
		Text:    "B",
	})

	const tenantID int64 = 200
	client, _ := email.GetHistoryClient(ctx, tenantID)

	// Send 3 emails
	_, _ = client.SendTemplate(ctx, email.SendTemplateRequest{
		TemplateID: "type_a",
		Recipient:  email.NewAddress("alice@example.com", "Alice"),
	})
	_, _ = client.SendTemplate(ctx, email.SendTemplateRequest{
		TemplateID: "type_b",
		Recipient:  email.NewAddress("bob@example.com", "Bob"),
	})
	_, _ = client.SendTemplate(ctx, email.SendTemplateRequest{
		TemplateID: "type_a",
		Recipient:  email.NewAddress("alice@example.com", "Alice"),
	})

	// Filter by recipient
	aliceList := client.List(email.ListFilter{RecipientEmail: "alice@example.com"})
	if len(aliceList) != 2 {
		t.Errorf("expected 2 emails for alice, got %d", len(aliceList))
	}

	// Filter by template
	bList := client.List(email.ListFilter{TemplateID: "type_b"})
	if len(bList) != 1 {
		t.Errorf("expected 1 email for type_b, got %d", len(bList))
	}

	// Filter with limit
	limitList := client.List(email.ListFilter{Limit: 2})
	if len(limitList) != 2 {
		t.Errorf("expected limit of 2, got %d", len(limitList))
	}

	// Order should be newest first
	all := client.List(email.ListFilter{})
	if len(all) != 3 {
		t.Fatalf("expected 3 emails total, got %d", len(all))
	}
	if all[0].RecipientEmail != "alice@example.com" || all[1].RecipientEmail != "bob@example.com" {
		t.Errorf("expected newest first order, got %+v", all)
	}
}

func TestHistory_Pruning(t *testing.T) {
	initHistoryTestPackage(t)
	ctx := context.Background()

	const tenantID int64 = 300
	client, _ := email.GetHistoryClient(ctx, tenantID)

	// Inject 1050 records into doc
	_ = client.Update(ctx, func(m proto.Message) error {
		doc := m.(*email.EmailHistoryProto)
		for i := 0; i < 1050; i++ {
			doc.Emails = append(doc.Emails, &email.EmailRecordProto{
				Id:             int64(i + 1),
				RecipientEmail: fmt.Sprintf("user%d@example.com", i),
				Subject:        fmt.Sprintf("Subject %d", i),
				Status:         email.EmailStatus_EMAIL_STATUS_SENT,
			})
		}
		return nil
	})

	mockSender := email.NewMockSender()
	email.SetDefaultSender(mockSender)

	_ = email.RegisterTemplate(email.TemplateDef{
		ID:      "prune_test",
		From:    email.NewAddress("noreply@example.com", "From"),
		Subject: "Prune",
		Text:    "Body",
	})

	// Sending next email triggers pruneHistory
	_, err := client.SendTemplate(ctx, email.SendTemplateRequest{
		TemplateID: "prune_test",
		Recipient:  email.NewAddress("new@example.com", "New"),
	})
	if err != nil {
		t.Fatalf("SendTemplate failed: %v", err)
	}

	all := client.List(email.ListFilter{})
	if len(all) > email.DefaultHistoryMaxEntries {
		t.Errorf("expected history length <= %d, got %d", email.DefaultHistoryMaxEntries, len(all))
	}
}
