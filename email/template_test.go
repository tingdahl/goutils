package email_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tingdahl/goutils/email"
)

func TestTemplateRegistry_Validation(t *testing.T) {
	email.ClearTemplatesForTesting()

	// Empty ID
	err := email.RegisterTemplate(email.TemplateDef{
		ID:      "",
		Subject: "Hello",
		HTML:    "<p>Hello</p>",
	})
	if !errors.Is(err, email.ErrEmptyTemplateID) {
		t.Errorf("expected ErrEmptyTemplateID, got %v", err)
	}

	// Empty Subject
	err = email.RegisterTemplate(email.TemplateDef{
		ID:      "test_tmpl",
		Subject: "",
		HTML:    "<p>Hello</p>",
	})
	if !errors.Is(err, email.ErrEmptySubject) {
		t.Errorf("expected ErrEmptySubject, got %v", err)
	}

	// Empty Body (both HTML and Text empty)
	err = email.RegisterTemplate(email.TemplateDef{
		ID:      "test_tmpl",
		Subject: "Hello",
		HTML:    "",
		Text:    "",
	})
	if !errors.Is(err, email.ErrEmptyTemplateBody) {
		t.Errorf("expected ErrEmptyTemplateBody, got %v", err)
	}

	// Invalid Subject syntax
	err = email.RegisterTemplate(email.TemplateDef{
		ID:      "test_tmpl",
		Subject: "Hello {{.Unclosed",
		HTML:    "<p>Hello</p>",
	})
	if err == nil {
		t.Errorf("expected error for invalid subject syntax, got nil")
	}

	// Invalid HTML syntax
	err = email.RegisterTemplate(email.TemplateDef{
		ID:      "test_tmpl",
		Subject: "Hello",
		HTML:    "<p>Hello {{.Unclosed</p>",
	})
	if err == nil {
		t.Errorf("expected error for invalid html syntax, got nil")
	}

	// Invalid Text syntax
	err = email.RegisterTemplate(email.TemplateDef{
		ID:      "test_tmpl",
		Subject: "Hello",
		Text:    "Hello {{.Unclosed",
	})
	if err == nil {
		t.Errorf("expected error for invalid text syntax, got nil")
	}
}

func TestTemplateRegistry_RegistrationAndRender(t *testing.T) {
	email.ClearTemplatesForTesting()

	def := email.TemplateDef{
		ID:      "sub_reminder",
		From:    email.NewAddress("noreply@example.com", "Acme"),
		Subject: "Your subscription for {{.TenantName}} ends in {{.Days}} days",
		HTML:    "<h1>Hello {{.UserName}}</h1><p>Your subscription for {{.TenantName}} ends in {{.Days}} days.</p>",
		Text:    "Hello {{.UserName}},\nYour subscription for {{.TenantName}} ends in {{.Days}} days.",
	}

	if err := email.RegisterTemplate(def); err != nil {
		t.Fatalf("RegisterTemplate failed: %v", err)
	}

	tmpl, err := email.GetTemplate("sub_reminder")
	if err != nil {
		t.Fatalf("GetTemplate failed: %v", err)
	}
	if tmpl.ID() != "sub_reminder" {
		t.Errorf("expected template ID sub_reminder, got %s", tmpl.ID())
	}
	if tmpl.From().Email != "noreply@example.com" {
		t.Errorf("expected template from noreply@example.com, got %s", tmpl.From().Email)
	}

	data := map[string]any{
		"TenantName": "Acme Corp",
		"UserName":   "Alice",
		"Days":       30,
	}

	subject, html, text, err := tmpl.Render(data)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	expectedSub := "Your subscription for Acme Corp ends in 30 days"
	if subject != expectedSub {
		t.Errorf("expected subject %q, got %q", expectedSub, subject)
	}
	if !strings.Contains(html, "<h1>Hello Alice</h1>") {
		t.Errorf("expected html to contain '<h1>Hello Alice</h1>', got %s", html)
	}
	if !strings.Contains(text, "Hello Alice,\n") {
		t.Errorf("expected text to contain greeting, got %s", text)
	}
}

func TestTemplate_HTMLEscaping(t *testing.T) {
	email.ClearTemplatesForTesting()

	err := email.RegisterTemplate(email.TemplateDef{
		ID:      "escape_test",
		Subject: "Alert for {{.Name}}",
		HTML:    "<p>Message: {{.UserInput}}</p>",
		Text:    "Message: {{.UserInput}}",
	})
	if err != nil {
		t.Fatalf("RegisterTemplate failed: %v", err)
	}

	tmpl, _ := email.GetTemplate("escape_test")
	data := map[string]any{
		"Name":      "Bob",
		"UserInput": "<script>alert('xss')</script>",
	}

	_, html, text, err := tmpl.Render(data)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	// HTML template must contextual escape script tag
	if strings.Contains(html, "<script>") {
		t.Errorf("expected HTML to escape script tag, got %s", html)
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Errorf("expected HTML to contain escaped entity &lt;script&gt;, got %s", html)
	}

	// Plain text should keep characters verbatim
	if !strings.Contains(text, "<script>alert('xss')</script>") {
		t.Errorf("expected text to keep verbatim script tag, got %s", text)
	}
}

func TestTemplate_HelperFunctions(t *testing.T) {
	email.ClearTemplatesForTesting()

	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	nowMs := now.UnixMilli()

	err := email.RegisterTemplate(email.TemplateDef{
		ID:      "helpers_test",
		Subject: "Invoice {{upper .InvoiceCode}}",
		HTML:    "<p>User: {{lower .Email}}</p><p>Trimmed: {{trim .Padded}}</p><p>Date: {{formatDate .DateMs \"2006-01-02\"}}</p>",
	})
	if err != nil {
		t.Fatalf("RegisterTemplate failed: %v", err)
	}

	tmpl, _ := email.GetTemplate("helpers_test")
	data := map[string]any{
		"InvoiceCode": "inv-42",
		"Email":       "ALICE@EXAMPLE.COM",
		"Padded":      "   spaced   ",
		"DateMs":      nowMs,
	}

	sub, html, _, err := tmpl.Render(data)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	if sub != "Invoice INV-42" {
		t.Errorf("expected upper subject 'Invoice INV-42', got %q", sub)
	}
	if !strings.Contains(html, "User: alice@example.com") {
		t.Errorf("expected lower email in html, got %s", html)
	}
	if !strings.Contains(html, "Trimmed: spaced") {
		t.Errorf("expected trimmed text in html, got %s", html)
	}
	if !strings.Contains(html, "Date: 2026-10-31") {
		t.Errorf("expected formatted date in html, got %s", html)
	}
}

func TestTemplate_MustRegisterPanic(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Errorf("expected panic on invalid template registration, got none")
		}
	}()

	email.MustRegisterTemplate(email.TemplateDef{
		ID: "",
	})
}

func TestTemplate_GetNotFound(t *testing.T) {
	email.ClearTemplatesForTesting()

	_, err := email.GetTemplate("non_existent")
	if !errors.Is(err, email.ErrTemplateNotFound) {
		t.Errorf("expected ErrTemplateNotFound, got %v", err)
	}
}
