package email

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/mail"
	"path/filepath"
	"strings"
	"sync"

	"github.com/tingdahl/goutils/config"
)

var (
	ErrNilMessage          = errors.New("email: message cannot be nil")
	ErrMissingFrom         = errors.New("email: 'from' address is required")
	ErrMissingTo           = errors.New("email: at least one 'to' recipient is required")
	ErrMissingSubject      = errors.New("email: subject cannot be empty")
	ErrMissingBody         = errors.New("email: at least one of text or html body is required")
	ErrInvalidEmailAddress = errors.New("email: invalid email address")
)

// Address represents an email address with an optional display name.
type Address struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

// String returns the formatted address (e.g. "John Doe <john@example.com>" or "john@example.com").
func (a Address) String() string {
	if a.Name == "" {
		return a.Email
	}
	addr := mail.Address{
		Name:    a.Name,
		Address: a.Email,
	}
	return addr.String()
}

// Validate checks whether the address contains a valid email.
func (a Address) Validate() error {
	trimmed := strings.TrimSpace(a.Email)
	if trimmed == "" {
		return ErrInvalidEmailAddress
	}
	// Use standard mail.ParseAddress validation
	parsed, err := mail.ParseAddress(a.String())
	if err != nil || parsed.Address == "" {
		return fmt.Errorf("%w: %q", ErrInvalidEmailAddress, a.Email)
	}
	return nil
}

// NewAddress creates a new Address.
func NewAddress(email, name string) Address {
	return Address{
		Email: strings.TrimSpace(email),
		Name:  strings.TrimSpace(name),
	}
}

// ParseAddress parses a single address string (e.g., "Alice <alice@example.com>" or "alice@example.com").
func ParseAddress(address string) (Address, error) {
	parsed, err := mail.ParseAddress(strings.TrimSpace(address))
	if err != nil {
		return Address{}, fmt.Errorf("%w: %v", ErrInvalidEmailAddress, err)
	}
	return Address{
		Email: parsed.Address,
		Name:  parsed.Name,
	}, nil
}

// ParseAddresses parses a list of address strings.
func ParseAddresses(addresses ...string) ([]Address, error) {
	var result []Address
	for _, addr := range addresses {
		a, err := ParseAddress(addr)
		if err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, nil
}

// Attachment represents an email attachment.
type Attachment struct {
	Name    string `json:"name"`
	Type    string `json:"type,omitempty"`
	Content []byte `json:"content"`
}

// ContentType returns the MIME content type of the attachment, inferring it from the Name if empty.
func (a Attachment) ContentType() string {
	if a.Type != "" {
		return a.Type
	}
	ext := filepath.Ext(a.Name)
	if ext != "" {
		if ct := mime.TypeByExtension(ext); ct != "" {
			return ct
		}
	}
	return "application/octet-stream"
}

// Header represents a custom email header.
type Header struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Message represents an email message to be sent.
type Message struct {
	From        Address           `json:"from"`
	To          []Address         `json:"to"`
	CC          []Address         `json:"cc,omitempty"`
	BCC         []Address         `json:"bcc,omitempty"`
	ReplyTo     *Address          `json:"reply_to,omitempty"`
	Subject     string            `json:"subject"`
	Text        string            `json:"text,omitempty"`
	HTML        string            `json:"html,omitempty"`
	Attachments []Attachment      `json:"attachments,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
}

// Validate validates that the message has all necessary fields.
func (m *Message) Validate() error {
	if m == nil {
		return ErrNilMessage
	}
	if err := m.From.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrMissingFrom, err)
	}
	if len(m.To) == 0 {
		return ErrMissingTo
	}
	for i, to := range m.To {
		if err := to.Validate(); err != nil {
			return fmt.Errorf("email: invalid 'to' recipient at index %d: %w", i, err)
		}
	}
	for i, cc := range m.CC {
		if err := cc.Validate(); err != nil {
			return fmt.Errorf("email: invalid 'cc' recipient at index %d: %w", i, err)
		}
	}
	for i, bcc := range m.BCC {
		if err := bcc.Validate(); err != nil {
			return fmt.Errorf("email: invalid 'bcc' recipient at index %d: %w", i, err)
		}
	}
	if m.ReplyTo != nil {
		if err := m.ReplyTo.Validate(); err != nil {
			return fmt.Errorf("email: invalid 'reply_to' address: %w", err)
		}
	}
	if strings.TrimSpace(m.Subject) == "" {
		return ErrMissingSubject
	}
	if strings.TrimSpace(m.Text) == "" && strings.TrimSpace(m.HTML) == "" {
		return ErrMissingBody
	}
	return nil
}

// Clone creates a deep copy of the Message.
func (m *Message) Clone() *Message {
	if m == nil {
		return nil
	}
	c := *m

	if m.To != nil {
		c.To = make([]Address, len(m.To))
		copy(c.To, m.To)
	}
	if m.CC != nil {
		c.CC = make([]Address, len(m.CC))
		copy(c.CC, m.CC)
	}
	if m.BCC != nil {
		c.BCC = make([]Address, len(m.BCC))
		copy(c.BCC, m.BCC)
	}
	if m.ReplyTo != nil {
		rt := *m.ReplyTo
		c.ReplyTo = &rt
	}
	if m.Attachments != nil {
		c.Attachments = make([]Attachment, len(m.Attachments))
		for i, a := range m.Attachments {
			c.Attachments[i] = a
			if a.Content != nil {
				c.Attachments[i].Content = make([]byte, len(a.Content))
				copy(c.Attachments[i].Content, a.Content)
			}
		}
	}
	if m.Headers != nil {
		c.Headers = make(map[string]string, len(m.Headers))
		for k, v := range m.Headers {
			c.Headers[k] = v
		}
	}
	return &c
}

// ApplyOverride applies recipient override logic.
// If overrideAddr is not empty, all CC and BCC recipients are dropped,
// and the only To recipient is set to overrideAddr.
func (m *Message) ApplyOverride(overrideAddr string) *Message {
	trimmed := strings.TrimSpace(overrideAddr)
	if trimmed == "" {
		return m
	}

	cloned := m.Clone()
	cloned.To = []Address{NewAddress(trimmed, "")}
	cloned.CC = nil
	cloned.BCC = nil
	return cloned
}

// Sender defines the common interface for sending email messages.
type Sender interface {
	Send(ctx context.Context, msg *Message) error
}

var (
	globalOverrideMu  sync.RWMutex
	globalOverrideTo  string
	globalOverrideSet bool

	defaultSenderMu sync.RWMutex
	defaultSender   Sender
)

// SetGlobalOverrideTo sets the global recipient override address programmatically.
// When set, all outgoing emails will have CC and BCC dropped, and only be sent to this address.
func SetGlobalOverrideTo(addr string) {
	globalOverrideMu.Lock()
	defer globalOverrideMu.Unlock()
	globalOverrideTo = strings.TrimSpace(addr)
	globalOverrideSet = true
}

// ResetGlobalOverrideTo clears any programmatically set global recipient override address,
// restoring behavior to read from goutils/config (EMAIL_OVERRIDE_TO).
func ResetGlobalOverrideTo() {
	globalOverrideMu.Lock()
	defer globalOverrideMu.Unlock()
	globalOverrideTo = ""
	globalOverrideSet = false
}

// GetGlobalOverrideTo returns the effective global recipient override address.
// If set programmatically via SetGlobalOverrideTo, that address is returned.
// Otherwise, it reads from config.GetEmailOverrideTo().
func GetGlobalOverrideTo() string {
	globalOverrideMu.RLock()
	isSet := globalOverrideSet
	val := globalOverrideTo
	globalOverrideMu.RUnlock()

	if isSet {
		return val
	}
	return config.GetEmailOverrideTo()
}

// SetDefaultSender sets the package-level default Sender.
func SetDefaultSender(s Sender) {
	defaultSenderMu.Lock()
	defer defaultSenderMu.Unlock()
	defaultSender = s
}

// DefaultSender returns the package-level default Sender, or initializes a ScalewaySender
// from configuration if none was explicitly set.
func DefaultSender() (Sender, error) {
	defaultSenderMu.RLock()
	s := defaultSender
	defaultSenderMu.RUnlock()
	if s != nil {
		return s, nil
	}

	defaultSenderMu.Lock()
	defer defaultSenderMu.Unlock()
	if defaultSender != nil {
		return defaultSender, nil
	}

	sender, err := NewScalewaySender()
	if err != nil {
		return nil, fmt.Errorf("email: failed to initialize default scaleway sender: %w", err)
	}
	defaultSender = sender
	return defaultSender, nil
}

// Send dispatches an email message using the package-level default Sender.
func Send(ctx context.Context, msg *Message) error {
	s, err := DefaultSender()
	if err != nil {
		return err
	}
	return s.Send(ctx, msg)
}
