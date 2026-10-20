package email

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tingdahl/goutils/config"
)

func TestNewScalewaySender(t *testing.T) {
	// 1. Missing secret key
	_, err := NewScalewaySender(
		WithSecretKey(""),
		WithProjectID("proj-123"),
	)
	if err == nil || !strings.Contains(err.Error(), "secret key is required") {
		t.Errorf("expected missing secret key error, got %v", err)
	}

	// 2. Missing project ID
	_, err = NewScalewaySender(
		WithSecretKey("sec-123"),
		WithProjectID(""),
	)
	if err == nil || !strings.Contains(err.Error(), "project ID is required") {
		t.Errorf("expected missing project ID error, got %v", err)
	}

	// 3. Valid construction with explicit options
	sender, err := NewScalewaySender(
		WithSecretKey("test-secret"),
		WithProjectID("test-project"),
		WithRegion("nl-ams"),
		WithBaseURL("https://custom.scaleway.com"),
		WithOverrideTo("trap@example.com"),
	)
	if err != nil {
		t.Fatalf("unexpected error creating sender: %v", err)
	}
	if sender.secretKey != "test-secret" || sender.projectID != "test-project" {
		t.Errorf("credentials mismatch: %+v", sender)
	}
	if sender.region != "nl-ams" {
		t.Errorf("region mismatch: %s", sender.region)
	}
	if sender.baseURL != "https://custom.scaleway.com" {
		t.Errorf("baseURL mismatch: %s", sender.baseURL)
	}
	if sender.overrideTo != "trap@example.com" {
		t.Errorf("overrideTo mismatch: %s", sender.overrideTo)
	}

	// 4. Default region fallback to config.DefaultSCWRegion ("nl-ams")
	senderDefaultRegion, err := NewScalewaySender(
		WithSecretKey("test-secret"),
		WithProjectID("test-project"),
		WithRegion(""),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if senderDefaultRegion.region != config.DefaultSCWRegion {
		t.Errorf("expected default region %q, got %q", config.DefaultSCWRegion, senderDefaultRegion.region)
	}

	// Shorthand region normalization ("ams" -> "nl-ams")
	senderNormalizedRegion, err := NewScalewaySender(
		WithSecretKey("test-secret"),
		WithProjectID("test-project"),
		WithRegion("ams"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if senderNormalizedRegion.region != "nl-ams" {
		t.Errorf("expected normalized region 'nl-ams', got %q", senderNormalizedRegion.region)
	}

	// 5. Construction via ConfigManager
	cm := config.NewConfigManager(func(k string) string {
		switch k {
		case config.EnvSCWSecretKey:
			return "cm-secret"
		case config.EnvSCWProjectID:
			return "cm-proj"
		case config.EnvSCWRegion:
			return "fr-par"
		case config.EnvEmailOverrideTo:
			return "cm-override@example.com"
		default:
			return ""
		}
	})
	senderCM, err := NewScalewaySender(WithConfig(cm))
	if err != nil {
		t.Fatalf("NewScalewaySender with ConfigManager failed: %v", err)
	}
	if senderCM.secretKey != "cm-secret" || senderCM.projectID != "cm-proj" {
		t.Errorf("ConfigManager credentials not applied: %+v", senderCM)
	}
	if senderCM.overrideTo != "cm-override@example.com" {
		t.Errorf("ConfigManager overrideTo not applied: %+v", senderCM)
	}
}

func TestScalewaySender_Send_Success(t *testing.T) {
	var capturedReq scwCreateEmailRequest
	var capturedAuthHeader string
	var capturedContentType string
	var capturedMethod string
	var capturedPath string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		capturedAuthHeader = r.Header.Get("X-Auth-Token")
		capturedContentType = r.Header.Get("Content-Type")

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := json.Unmarshal(body, &capturedReq); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"emails":[{"id":"1111-2222","status":"new"}]}`))
	}))
	defer server.Close()

	sender, err := NewScalewaySender(
		WithSecretKey("secret-token-xyz"),
		WithProjectID("project-uuid-123"),
		WithRegion("fr-par"),
		WithBaseURL(server.URL),
		WithHTTPClient(server.Client()),
	)
	if err != nil {
		t.Fatalf("failed to create sender: %v", err)
	}

	msg := &Message{
		From:    NewAddress("sender@verified-domain.com", "Acme Admin"),
		To:      []Address{NewAddress("recipient1@example.com", "Bob"), NewAddress("recipient2@example.com", "")},
		CC:      []Address{NewAddress("cc1@example.com", "Charlie")},
		BCC:     []Address{NewAddress("bcc1@example.com", "Dave")},
		Subject: "Welcome to Our Platform",
		Text:    "Hello Bob, thanks for signing up!",
		HTML:    "<h1>Hello Bob!</h1><p>Thanks for signing up!</p>",
	}

	ctx := context.Background()
	if err := sender.Send(ctx, msg); err != nil {
		t.Fatalf("Send() failed: %v", err)
	}

	// Verify HTTP layer
	if capturedMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", capturedMethod)
	}
	if capturedPath != "/transactional-email/v1alpha1/regions/fr-par/emails" {
		t.Errorf("path = %s, want /transactional-email/v1alpha1/regions/fr-par/emails", capturedPath)
	}
	if capturedAuthHeader != "secret-token-xyz" {
		t.Errorf("X-Auth-Token = %s, want secret-token-xyz", capturedAuthHeader)
	}
	if capturedContentType != "application/json" {
		t.Errorf("Content-Type = %s, want application/json", capturedContentType)
	}

	// Verify Payload
	if capturedReq.ProjectID != "project-uuid-123" {
		t.Errorf("project_id = %s, want project-uuid-123", capturedReq.ProjectID)
	}
	if capturedReq.From.Email != "sender@verified-domain.com" || capturedReq.From.Name != "Acme Admin" {
		t.Errorf("from address mismatch: %+v", capturedReq.From)
	}
	if len(capturedReq.To) != 2 {
		t.Fatalf("expected 2 To recipients, got %d", len(capturedReq.To))
	}
	if capturedReq.To[0].Email != "recipient1@example.com" || capturedReq.To[0].Name != "Bob" {
		t.Errorf("first To recipient mismatch: %+v", capturedReq.To[0])
	}
	if len(capturedReq.CC) != 1 || capturedReq.CC[0].Email != "cc1@example.com" {
		t.Errorf("CC mismatch: %+v", capturedReq.CC)
	}
	if len(capturedReq.BCC) != 1 || capturedReq.BCC[0].Email != "bcc1@example.com" {
		t.Errorf("BCC mismatch: %+v", capturedReq.BCC)
	}
	if capturedReq.Subject != "Welcome to Our Platform" {
		t.Errorf("subject mismatch: %s", capturedReq.Subject)
	}
	if capturedReq.Text != "Hello Bob, thanks for signing up!" {
		t.Errorf("text mismatch: %s", capturedReq.Text)
	}
	if capturedReq.HTML != "<h1>Hello Bob!</h1><p>Thanks for signing up!</p>" {
		t.Errorf("html mismatch: %s", capturedReq.HTML)
	}
}

func TestScalewaySender_Send_WithOverride(t *testing.T) {
	ResetGlobalOverrideTo()
	defer ResetGlobalOverrideTo()

	var capturedReq scwCreateEmailRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedReq)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"emails":[{"id":"abc"}]}`))
	}))
	defer server.Close()

	// 1. Test with global override set via SetGlobalOverrideTo
	SetGlobalOverrideTo("blackhole@mycompany.dev")

	sender, err := NewScalewaySender(
		WithSecretKey("sec-123"),
		WithProjectID("proj-123"),
		WithBaseURL(server.URL),
		WithHTTPClient(server.Client()),
	)
	if err != nil {
		t.Fatalf("failed to create sender: %v", err)
	}

	msg := &Message{
		From:    NewAddress("support@company.com", "Support"),
		To:      []Address{NewAddress("real-client1@client.com", "Client 1"), NewAddress("real-client2@client.com", "Client 2")},
		CC:      []Address{NewAddress("manager@company.com", "Manager")},
		BCC:     []Address{NewAddress("audit@company.com", "Audit")},
		Subject: "Real production alert",
		Text:    "Important alert message",
	}

	if err := sender.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send() failed: %v", err)
	}

	// Assertions for override behavior:
	// "If that is set, all cc and bcc are dropped, and the only to - address is the address in the override address."
	if len(capturedReq.To) != 1 {
		t.Fatalf("expected exactly 1 To recipient, got %d", len(capturedReq.To))
	}
	if capturedReq.To[0].Email != "blackhole@mycompany.dev" {
		t.Errorf("expected To email %q, got %q", "blackhole@mycompany.dev", capturedReq.To[0].Email)
	}
	if len(capturedReq.CC) != 0 {
		t.Errorf("expected all CC recipients to be dropped, got: %+v", capturedReq.CC)
	}
	if len(capturedReq.BCC) != 0 {
		t.Errorf("expected all BCC recipients to be dropped, got: %+v", capturedReq.BCC)
	}

	// 2. Test instance-level override takes precedence over global override
	senderWithExplicitOverride, err := NewScalewaySender(
		WithSecretKey("sec-123"),
		WithProjectID("proj-123"),
		WithBaseURL(server.URL),
		WithHTTPClient(server.Client()),
		WithOverrideTo("instance-override@mycompany.dev"),
	)
	if err != nil {
		t.Fatalf("failed to create sender: %v", err)
	}

	if err := senderWithExplicitOverride.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send() failed: %v", err)
	}

	if len(capturedReq.To) != 1 || capturedReq.To[0].Email != "instance-override@mycompany.dev" {
		t.Errorf("instance override was not used: %+v", capturedReq.To)
	}
	if len(capturedReq.CC) != 0 || len(capturedReq.BCC) != 0 {
		t.Errorf("CC or BCC was not dropped in instance override: CC=%+v, BCC=%+v", capturedReq.CC, capturedReq.BCC)
	}
}

func TestScalewaySender_Send_AttachmentsAndHeaders(t *testing.T) {
	var capturedReq scwCreateEmailRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedReq)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"emails":[{"id":"abc"}]}`))
	}))
	defer server.Close()

	sender, err := NewScalewaySender(
		WithSecretKey("sec-123"),
		WithProjectID("proj-123"),
		WithBaseURL(server.URL),
		WithHTTPClient(server.Client()),
	)
	if err != nil {
		t.Fatalf("failed to create sender: %v", err)
	}

	pdfContent := []byte("%PDF-1.4 mock pdf content")
	replyToAddr := NewAddress("reply-here@company.com", "Support Desk")

	msg := &Message{
		From:    NewAddress("noreply@company.com", "No Reply"),
		To:      []Address{NewAddress("customer@example.com", "Customer")},
		ReplyTo: &replyToAddr,
		Subject: "Your Invoice",
		Text:    "Please find invoice attached.",
		Attachments: []Attachment{
			{
				Name:    "invoice.pdf",
				Type:    "application/pdf",
				Content: pdfContent,
			},
		},
		Headers: map[string]string{
			"X-Tracking-ID": "track-12345",
		},
	}

	if err := sender.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send() failed: %v", err)
	}

	// Verify attachments
	if len(capturedReq.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(capturedReq.Attachments))
	}
	att := capturedReq.Attachments[0]
	if att.Name != "invoice.pdf" || att.Type != "application/pdf" {
		t.Errorf("attachment metadata mismatch: %+v", att)
	}
	decodedContent, err := base64.StdEncoding.DecodeString(att.Content)
	if err != nil {
		t.Fatalf("failed to base64 decode attachment content: %v", err)
	}
	if string(decodedContent) != string(pdfContent) {
		t.Errorf("attachment content mismatch: got %q, want %q", string(decodedContent), string(pdfContent))
	}

	// Verify additional headers
	headersMap := make(map[string]string)
	for _, h := range capturedReq.AdditionalHeaders {
		headersMap[h.Key] = h.Value
	}
	if headersMap["Reply-To"] != replyToAddr.String() {
		t.Errorf("Reply-To header mismatch: %s", headersMap["Reply-To"])
	}
	if headersMap["X-Tracking-ID"] != "track-12345" {
		t.Errorf("X-Tracking-ID header mismatch: %s", headersMap["X-Tracking-ID"])
	}
}

func TestScalewaySender_Send_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{
			"message": "Domain sender domain is not verified",
			"type": "invalid_argument",
			"help_message": "Please add DNS TXT records to verify domain"
		}`))
	}))
	defer server.Close()

	sender, err := NewScalewaySender(
		WithSecretKey("sec-123"),
		WithProjectID("proj-123"),
		WithBaseURL(server.URL),
		WithHTTPClient(server.Client()),
	)
	if err != nil {
		t.Fatalf("failed to create sender: %v", err)
	}

	msg := &Message{
		From:    NewAddress("unverified@domain.com", "Test"),
		To:      []Address{NewAddress("to@example.com", "To")},
		Subject: "Test",
		Text:    "Body",
	}

	err = sender.Send(context.Background(), msg)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "status 400") {
		t.Errorf("expected error to mention status 400, got %q", errMsg)
	}
	if !strings.Contains(errMsg, "Domain sender domain is not verified") {
		t.Errorf("expected error to contain API message, got %q", errMsg)
	}
	if !strings.Contains(errMsg, "Please add DNS TXT records to verify domain") {
		t.Errorf("expected error to contain help message, got %q", errMsg)
	}
}

func TestScalewaySender_Send_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sender, err := NewScalewaySender(
		WithSecretKey("sec-123"),
		WithProjectID("proj-123"),
		WithBaseURL(server.URL),
		WithHTTPClient(server.Client()),
	)
	if err != nil {
		t.Fatalf("failed to create sender: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	msg := &Message{
		From:    NewAddress("from@example.com", "From"),
		To:      []Address{NewAddress("to@example.com", "To")},
		Subject: "Test",
		Text:    "Body",
	}

	err = sender.Send(ctx, msg)
	if err == nil {
		t.Fatal("expected context canceled error, got nil")
	}
}

func TestScalewaySender_Send_NilMessage(t *testing.T) {
	sender, err := NewScalewaySender(
		WithSecretKey("sec-123"),
		WithProjectID("proj-123"),
	)
	if err != nil {
		t.Fatalf("failed to create sender: %v", err)
	}

	if err := sender.Send(context.Background(), nil); !errors.Is(err, ErrNilMessage) {
		t.Errorf("expected ErrNilMessage, got %v", err)
	}
}

func TestDefaultSender_LazyInitialization(t *testing.T) {
	// Clear any existing default sender
	SetDefaultSender(nil)

	// In an environment without SCW credentials, DefaultSender returns an error
	os.Unsetenv(config.EnvSCWSecretKey)
	os.Unsetenv(config.EnvSCWProjectID)
	os.Unsetenv(config.EnvSCWDefaultProjectID)

	_, err := DefaultSender()
	if err == nil {
		t.Error("expected error initializing DefaultSender without credentials")
	}

	// Set mock default sender
	mock := NewMockSender()
	SetDefaultSender(mock)

	ds, err := DefaultSender()
	if err != nil {
		t.Fatalf("DefaultSender returned error: %v", err)
	}
	if ds != mock {
		t.Error("DefaultSender did not return explicit default sender")
	}
}
