package email

import (
	"context"
	"sync"
)

// MockSender is an in-memory implementation of Sender useful for unit testing.
type MockSender struct {
	mu       sync.Mutex
	sent     []*Message
	sendFunc func(ctx context.Context, msg *Message) error
}

// NewMockSender creates a new MockSender.
func NewMockSender() *MockSender {
	return &MockSender{
		sent: make([]*Message, 0),
	}
}

// Send records the email message and executes sendFunc if defined.
// If an override is configured, the recorded message reflects the effective overridden message.
func (m *MockSender) Send(ctx context.Context, msg *Message) error {
	if msg == nil {
		return ErrNilMessage
	}

	override := GetGlobalOverrideTo()
	effectiveMsg := msg.ApplyOverride(override)

	if err := effectiveMsg.Validate(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.sendFunc != nil {
		if err := m.sendFunc(ctx, effectiveMsg); err != nil {
			return err
		}
	}

	m.sent = append(m.sent, effectiveMsg.Clone())
	return nil
}

// SentMessages returns a copy of all recorded messages.
func (m *MockSender) SentMessages() []*Message {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make([]*Message, len(m.sent))
	for i, msg := range m.sent {
		result[i] = msg.Clone()
	}
	return result
}

// LastSentMessage returns the most recently sent message, or nil if none sent.
func (m *MockSender) LastSentMessage() *Message {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.sent) == 0 {
		return nil
	}
	return m.sent[len(m.sent)-1].Clone()
}

// Reset clears all recorded sent messages and custom send functions.
func (m *MockSender) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.sent = make([]*Message, 0)
	m.sendFunc = nil
}

// SetSendFunc sets a custom callback function executed on Send.
func (m *MockSender) SetSendFunc(fn func(ctx context.Context, msg *Message) error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.sendFunc = fn
}
