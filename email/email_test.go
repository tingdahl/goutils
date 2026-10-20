package email

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/tingdahl/goutils/config"
)

func TestAddress_Validation(t *testing.T) {
	tests := []struct {
		name    string
		addr    Address
		wantErr bool
	}{
		{
			name:    "valid simple email",
			addr:    Address{Email: "alice@example.com"},
			wantErr: false,
		},
		{
			name:    "valid email with name",
			addr:    Address{Email: "alice@example.com", Name: "Alice Smith"},
			wantErr: false,
		},
		{
			name:    "empty email",
			addr:    Address{Email: ""},
			wantErr: true,
		},
		{
			name:    "whitespace only",
			addr:    Address{Email: "   "},
			wantErr: true,
		},
		{
			name:    "invalid email format",
			addr:    Address{Email: "not-an-email"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.addr.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Address.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestAddress_String(t *testing.T) {
	a1 := Address{Email: "user@example.com"}
	if got := a1.String(); got != "user@example.com" {
		t.Errorf("Address.String() = %q, want %q", got, "user@example.com")
	}

	a2 := Address{Email: "user@example.com", Name: "User One"}
	if got := a2.String(); got != "\"User One\" <user@example.com>" {
		t.Errorf("Address.String() = %q, want %q", got, "\"User One\" <user@example.com>")
	}
}

func TestAddress_ParseAddress(t *testing.T) {
	tests := []struct {
		input     string
		wantEmail string
		wantName  string
		wantErr   bool
	}{
		{
			input:     "user@example.com",
			wantEmail: "user@example.com",
			wantName:  "",
			wantErr:   false,
		},
		{
			input:     "Alice Smith <alice@example.com>",
			wantEmail: "alice@example.com",
			wantName:  "Alice Smith",
			wantErr:   false,
		},
		{
			input:   "invalid",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		addr, err := ParseAddress(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParseAddress(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			continue
		}
		if !tt.wantErr {
			if addr.Email != tt.wantEmail || addr.Name != tt.wantName {
				t.Errorf("ParseAddress(%q) = %+v, want Email=%q, Name=%q", tt.input, addr, tt.wantEmail, tt.wantName)
			}
		}
	}
}

func TestAddress_ParseAddresses(t *testing.T) {
	list, err := ParseAddresses("Alice <alice@example.com>", "bob@example.com")
	if err != nil {
		t.Fatalf("ParseAddresses failed: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 addresses, got %d", len(list))
	}
	if list[0].Email != "alice@example.com" || list[0].Name != "Alice" {
		t.Errorf("first address mismatch: %+v", list[0])
	}
	if list[1].Email != "bob@example.com" {
		t.Errorf("second address mismatch: %+v", list[1])
	}

	_, err = ParseAddresses("invalid", "bob@example.com")
	if err == nil {
		t.Error("expected error for invalid address in list")
	}
}

func TestAttachment_ContentType(t *testing.T) {
	a1 := Attachment{Name: "document.pdf", Type: "application/pdf"}
	if got := a1.ContentType(); got != "application/pdf" {
		t.Errorf("ContentType() = %q, want %q", got, "application/pdf")
	}

	a2 := Attachment{Name: "picture.png"}
	if got := a2.ContentType(); got != "image/png" {
		t.Errorf("ContentType() inferred = %q, want %q", got, "image/png")
	}

	a3 := Attachment{Name: "data.unknownext"}
	if got := a3.ContentType(); got != "application/octet-stream" {
		t.Errorf("ContentType() fallback = %q, want %q", got, "application/octet-stream")
	}
}

func TestMessage_Validate(t *testing.T) {
	validFrom := NewAddress("sender@example.com", "Sender")
	validTo := NewAddress("recipient@example.com", "Recipient")

	var nilMsg *Message
	if err := nilMsg.Validate(); !errors.Is(err, ErrNilMessage) {
		t.Errorf("expected ErrNilMessage, got %v", err)
	}

	tests := []struct {
		name    string
		msg     Message
		wantErr error
	}{
		{
			name: "valid text message",
			msg: Message{
				From:    validFrom,
				To:      []Address{validTo},
				Subject: "Test Subject",
				Text:    "Hello world",
			},
			wantErr: nil,
		},
		{
			name: "valid html message",
			msg: Message{
				From:    validFrom,
				To:      []Address{validTo},
				Subject: "Test Subject",
				HTML:    "<p>Hello</p>",
			},
			wantErr: nil,
		},
		{
			name: "missing from",
			msg: Message{
				To:      []Address{validTo},
				Subject: "Test",
				Text:    "Body",
			},
			wantErr: ErrMissingFrom,
		},
		{
			name: "missing to",
			msg: Message{
				From:    validFrom,
				Subject: "Test",
				Text:    "Body",
			},
			wantErr: ErrMissingTo,
		},
		{
			name: "missing subject",
			msg: Message{
				From: validFrom,
				To:   []Address{validTo},
				Text: "Body",
			},
			wantErr: ErrMissingSubject,
		},
		{
			name: "missing body",
			msg: Message{
				From:    validFrom,
				To:      []Address{validTo},
				Subject: "Test",
			},
			wantErr: ErrMissingBody,
		},
		{
			name: "invalid cc address",
			msg: Message{
				From:    validFrom,
				To:      []Address{validTo},
				CC:      []Address{{Email: "invalid"}},
				Subject: "Test",
				Text:    "Body",
			},
			wantErr: ErrInvalidEmailAddress,
		},
		{
			name: "invalid bcc address",
			msg: Message{
				From:    validFrom,
				To:      []Address{validTo},
				BCC:     []Address{{Email: "invalid"}},
				Subject: "Test",
				Text:    "Body",
			},
			wantErr: ErrInvalidEmailAddress,
		},
		{
			name: "invalid reply-to address",
			msg: Message{
				From:    validFrom,
				To:      []Address{validTo},
				ReplyTo: &Address{Email: "invalid"},
				Subject: "Test",
				Text:    "Body",
			},
			wantErr: ErrInvalidEmailAddress,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.msg.Validate()
			if tt.wantErr == nil && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected error wrapping %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestMessage_Clone(t *testing.T) {
	orig := &Message{
		From:    NewAddress("from@example.com", "From"),
		To:      []Address{NewAddress("to@example.com", "To")},
		CC:      []Address{NewAddress("cc@example.com", "CC")},
		BCC:     []Address{NewAddress("bcc@example.com", "BCC")},
		ReplyTo: &Address{Email: "reply@example.com"},
		Subject: "Subject",
		Text:    "Body",
		Attachments: []Attachment{
			{Name: "test.txt", Type: "text/plain", Content: []byte("hello")},
		},
		Headers: map[string]string{"X-Custom": "val"},
	}

	cloned := orig.Clone()
	if cloned == nil {
		t.Fatal("cloned message is nil")
	}

	// Modify cloned
	cloned.To[0].Email = "modified@example.com"
	cloned.CC[0].Email = "modified-cc@example.com"
	cloned.Attachments[0].Content[0] = 'H'
	cloned.Headers["X-Custom"] = "modified-val"

	// Original must remain unaffected
	if orig.To[0].Email != "to@example.com" {
		t.Errorf("original To was mutated: %s", orig.To[0].Email)
	}
	if orig.CC[0].Email != "cc@example.com" {
		t.Errorf("original CC was mutated: %s", orig.CC[0].Email)
	}
	if string(orig.Attachments[0].Content) != "hello" {
		t.Errorf("original Attachment content was mutated: %s", string(orig.Attachments[0].Content))
	}
	if orig.Headers["X-Custom"] != "val" {
		t.Errorf("original Header was mutated: %s", orig.Headers["X-Custom"])
	}

	var nilMsg *Message
	if nilMsg.Clone() != nil {
		t.Error("expected nil clone for nil message")
	}
}

func TestMessage_ApplyOverride(t *testing.T) {
	orig := &Message{
		From:    NewAddress("from@example.com", "From"),
		To:      []Address{NewAddress("user1@example.com", "U1"), NewAddress("user2@example.com", "U2")},
		CC:      []Address{NewAddress("cc1@example.com", "CC1")},
		BCC:     []Address{NewAddress("bcc1@example.com", "BCC1")},
		Subject: "Important Notification",
		Text:    "Content",
	}

	// 1. With empty override address: message is unchanged
	noOverride := orig.ApplyOverride("")
	if len(noOverride.To) != 2 || len(noOverride.CC) != 1 || len(noOverride.BCC) != 1 {
		t.Errorf("ApplyOverride(\"\") modified recipients")
	}

	// 2. With override address: CC and BCC are dropped, only To is override address
	overridden := orig.ApplyOverride("dev-trap@example.com")

	if len(overridden.To) != 1 {
		t.Fatalf("expected exactly 1 To recipient, got %d", len(overridden.To))
	}
	if overridden.To[0].Email != "dev-trap@example.com" {
		t.Errorf("expected To recipient = %q, got %q", "dev-trap@example.com", overridden.To[0].Email)
	}
	if len(overridden.CC) != 0 {
		t.Errorf("expected CC to be dropped, got %d recipients", len(overridden.CC))
	}
	if len(overridden.BCC) != 0 {
		t.Errorf("expected BCC to be dropped, got %d recipients", len(overridden.BCC))
	}

	// Original must not be modified
	if len(orig.To) != 2 || len(orig.CC) != 1 || len(orig.BCC) != 1 {
		t.Errorf("original message was modified during ApplyOverride")
	}
}

func TestGlobalOverrideTo(t *testing.T) {
	ResetGlobalOverrideTo()
	defer ResetGlobalOverrideTo()

	// 1. Initially empty (unless env var set)
	os.Unsetenv(config.EnvEmailOverrideTo)
	if got := GetGlobalOverrideTo(); got != "" {
		t.Errorf("expected empty override, got %q", got)
	}

	// 2. Set programmatically
	SetGlobalOverrideTo("override-programmatic@example.com")
	if got := GetGlobalOverrideTo(); got != "override-programmatic@example.com" {
		t.Errorf("expected programmatic override, got %q", got)
	}

	// 3. Reset programmatic override
	ResetGlobalOverrideTo()
	if got := GetGlobalOverrideTo(); got != "" {
		t.Errorf("expected empty override after reset, got %q", got)
	}

	// 4. Fallback to config string (environment variable)
	os.Setenv(config.EnvEmailOverrideTo, "config-env@example.com")
	defer os.Unsetenv(config.EnvEmailOverrideTo)
	if got := GetGlobalOverrideTo(); got != "config-env@example.com" {
		t.Errorf("expected config env override, got %q", got)
	}

	// Programmatic override takes precedence over config
	SetGlobalOverrideTo("explicit-wins@example.com")
	if got := GetGlobalOverrideTo(); got != "explicit-wins@example.com" {
		t.Errorf("expected explicit programmatic to take precedence, got %q", got)
	}
	ResetGlobalOverrideTo()
}

func TestMockSender(t *testing.T) {
	ResetGlobalOverrideTo()
	defer ResetGlobalOverrideTo()

	mock := NewMockSender()
	SetDefaultSender(mock)

	msg := &Message{
		From:    NewAddress("sender@example.com", "Sender"),
		To:      []Address{NewAddress("to@example.com", "To")},
		CC:      []Address{NewAddress("cc@example.com", "CC")},
		Subject: "Test Mock",
		Text:    "Mock text",
	}

	ctx := context.Background()
	if err := Send(ctx, msg); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	sent := mock.SentMessages()
	if len(sent) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(sent))
	}
	if sent[0].Subject != "Test Mock" {
		t.Errorf("subject = %q, want 'Test Mock'", sent[0].Subject)
	}

	last := mock.LastSentMessage()
	if last == nil || last.Subject != "Test Mock" {
		t.Errorf("LastSentMessage failed: %+v", last)
	}

	// Test with override active
	SetGlobalOverrideTo("trap@example.com")
	msg2 := &Message{
		From:    NewAddress("sender@example.com", "Sender"),
		To:      []Address{NewAddress("to@example.com", "To")},
		CC:      []Address{NewAddress("cc@example.com", "CC")},
		Subject: "Test Override Mock",
		Text:    "Override body",
	}
	if err := Send(ctx, msg2); err != nil {
		t.Fatalf("Send with override failed: %v", err)
	}

	last2 := mock.LastSentMessage()
	if len(last2.To) != 1 || last2.To[0].Email != "trap@example.com" {
		t.Errorf("expected To to be overridden to trap@example.com, got: %+v", last2.To)
	}
	if len(last2.CC) != 0 {
		t.Errorf("expected CC to be dropped in mock sender, got: %+v", last2.CC)
	}

	// Test custom sendFunc
	mock.SetSendFunc(func(ctx context.Context, msg *Message) error {
		return errors.New("simulated send failure")
	})
	if err := Send(ctx, msg); err == nil {
		t.Error("expected error from sendFunc, got nil")
	}

	// Reset mock
	mock.Reset()
	if len(mock.SentMessages()) != 0 {
		t.Errorf("expected 0 messages after reset, got %d", len(mock.SentMessages()))
	}
	if mock.LastSentMessage() != nil {
		t.Errorf("expected nil LastSentMessage after reset")
	}
}
