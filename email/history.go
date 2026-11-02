//go:generate protoc --go_out=. --go_opt=paths=source_relative email_history.v1.proto

package email

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/tingdahl/goutils"
	"github.com/tingdahl/goutils/storage"
	"github.com/tingdahl/goutils/storage/docstore"
	"google.golang.org/protobuf/proto"
)

const (
	// DbObjectName is the storage object filename for the email history document.
	DbObjectName = "email_history.v1.pb.zst"

	// SchemaMinor is the current minor schema version supported by this package.
	SchemaMinor int32 = 0

	// DefaultHistoryMaxEntries is the maximum number of email records kept per tenant before pruning.
	DefaultHistoryMaxEntries = 1000

	// DefaultClaimTimeout is how long a worker claim remains valid before being eligible for reclaim.
	DefaultClaimTimeout = 15 * time.Minute
)

var (
	ErrAlreadySent      = errors.New("email: email with this deduplication key has already been sent")
	ErrCurrentlyClaimed = errors.New("email: email with this deduplication key is currently claimed by another worker")
	ErrRecordNotFound   = errors.New("email: history record not found")
)

var (
	historyInitOnce sync.Once
	historyRepo     *docstore.Repository[*HistoryClient]
	historyStore    storage.StorageClient
	historyPrefix   = "tenants"
)

// Init initializes the email history repository for the given storage client and tenant object prefix.
func Init(store storage.StorageClient, prefix string, opts ...docstore.RepositoryOption) error {
	if store == nil {
		return errors.New("email: storage client cannot be nil")
	}

	historyInitOnce.Do(func() {
		if strings.TrimSpace(prefix) != "" {
			historyPrefix = prefix
		}
		historyStore = store
		historyRepo = docstore.NewRepository(historyClientFactory, opts...)
	})
	return nil
}

// InitHistory is an alias for Init.
func InitHistory(store storage.StorageClient, prefix string, opts ...docstore.RepositoryOption) error {
	return Init(store, prefix, opts...)
}

// ResetForTesting resets the package-level repository and template state for test isolation.
func ResetForTesting() {
	historyInitOnce = sync.Once{}
	historyRepo = nil
	historyStore = nil
	historyPrefix = "tenants"
	ClearTemplatesForTesting()
	ResetGlobalOverrideTo()
}

func historyClientFactory(ctx context.Context, objectPath string) (*HistoryClient, error) {
	if historyStore == nil {
		return nil, errors.New("email: storage client cannot be nil")
	}
	if objectPath == "" {
		return nil, errors.New("email: object path cannot be empty")
	}

	client := &HistoryClient{}
	client.Document = docstore.Document{
		Client:               client,
		Storage:              historyStore,
		ObjectName:           objectPath,
		ProtoMsg:             &client.data,
		SupportedSchemaMinor: SchemaMinor,
	}

	if err := client.DoLoad(); err != nil {
		return nil, fmt.Errorf("email: failed to load email history from storage: %w", err)
	}

	return client, nil
}

// TenantEmailHistoryPath returns the storage object path for a given tenant ID.
func TenantEmailHistoryPath(tenantID int64) string {
	prefix := strings.Trim(historyPrefix, "/")
	if prefix == "" {
		return fmt.Sprintf("%d/%s", tenantID, DbObjectName)
	}
	return fmt.Sprintf("%s/%d/%s", prefix, tenantID, DbObjectName)
}

// GetHistoryClient retrieves the cached HistoryClient for the specified tenant ID.
func GetHistoryClient(ctx context.Context, tenantID int64) (*HistoryClient, error) {
	if historyRepo == nil {
		return nil, errors.New("email: history package not initialized: call email.Init() first")
	}
	if tenantID <= 0 {
		return nil, errors.New("email: tenant ID must be greater than zero")
	}

	client, err := historyRepo.Get(ctx, TenantEmailHistoryPath(tenantID))
	if err != nil {
		return nil, err
	}

	client.Rwlock.RLock()
	existingTenantID := client.data.TenantId
	client.Rwlock.RUnlock()

	if existingTenantID != 0 && existingTenantID != tenantID {
		return nil, fmt.Errorf("email: tenant ID mismatch: expected %d, got %d", tenantID, existingTenantID)
	}

	client.tenantID = tenantID
	return client, nil
}

// HistoryClient provides tenant-scoped email logging, deduplication, and synchronous sending.
type HistoryClient struct {
	docstore.Document
	tenantID int64
	data     EmailHistoryProto
}

// TenantID returns the tenant ID associated with this history client.
func (c *HistoryClient) TenantID() int64 {
	return c.tenantID
}

// ReadFromProto deserializes raw protobuf bytes into data.
func (c *HistoryClient) ReadFromProto(data []byte) error {
	proto.Reset(&c.data)
	if data == nil {
		return nil
	}
	return proto.Unmarshal(data, &c.data)
}

// GetSchemaMinorVersion returns the minor schema version from the protobuf message.
func (c *HistoryClient) GetSchemaMinorVersion() int32 {
	return c.data.SchemaMinorVersion
}

// StampVersion updates the tenant ID, schema version, commit hash, and timestamp on save.
func (c *HistoryClient) StampVersion(msg proto.Message, schemaMinor int32, commit string) {
	doc := msg.(*EmailHistoryProto)
	doc.TenantId = c.tenantID
	doc.SchemaMinorVersion = schemaMinor
	doc.LastModifiedByCommit = commit
	doc.UpdatedAtUnixMs = time.Now().UnixMilli()
}

// ListFilter configures filtering criteria when querying email records.
type ListFilter struct {
	RecipientEmail   string
	TemplateID       string
	Status           *EmailStatus
	DeduplicationKey string
	Limit            int
}

// List returns email records matching the filter criteria in reverse chronological order (newest first).
func (c *HistoryClient) List(filter ListFilter) []*EmailRecordProto {
	c.Rwlock.RLock()
	defer c.Rwlock.RUnlock()

	var result []*EmailRecordProto
	filterEmail := strings.TrimSpace(filter.RecipientEmail)
	filterTmpl := strings.TrimSpace(filter.TemplateID)
	filterDedup := strings.TrimSpace(filter.DeduplicationKey)

	for i := len(c.data.Emails) - 1; i >= 0; i-- {
		rec := c.data.Emails[i]
		if filterEmail != "" && !strings.EqualFold(rec.RecipientEmail, filterEmail) {
			continue
		}
		if filterTmpl != "" && rec.TemplateId != filterTmpl {
			continue
		}
		if filter.Status != nil && rec.Status != *filter.Status {
			continue
		}
		if filterDedup != "" && rec.DeduplicationKey != filterDedup {
			continue
		}

		result = append(result, proto.Clone(rec).(*EmailRecordProto))
		if filter.Limit > 0 && len(result) >= filter.Limit {
			break
		}
	}

	if result == nil {
		return []*EmailRecordProto{}
	}
	return result
}

// Get retrieves an email record by its int64 ID.
func (c *HistoryClient) Get(recordID int64) *EmailRecordProto {
	c.Rwlock.RLock()
	defer c.Rwlock.RUnlock()

	for _, rec := range c.data.Emails {
		if rec.Id == recordID {
			return proto.Clone(rec).(*EmailRecordProto)
		}
	}
	return nil
}

// GetByDeduplicationKey retrieves an email record by its deduplication key.
func (c *HistoryClient) GetByDeduplicationKey(dedupKey string) *EmailRecordProto {
	c.Rwlock.RLock()
	defer c.Rwlock.RUnlock()

	trimmed := strings.TrimSpace(dedupKey)
	if trimmed == "" {
		return nil
	}
	for _, rec := range c.data.Emails {
		if rec.DeduplicationKey == trimmed {
			return proto.Clone(rec).(*EmailRecordProto)
		}
	}
	return nil
}

// SendTemplateRequest defines parameters for rendering and sending an email template synchronously.
type SendTemplateRequest struct {
	TemplateID       string
	From             *Address
	Recipient        Address
	CC               []Address
	BCC              []Address
	ReplyTo          *Address
	Attachments      []Attachment
	Headers          map[string]string
	Data             any
	DeduplicationKey string
	Sender           Sender
	ClaimTimeout     time.Duration
}

// SendTemplate renders a registered template and synchronously dispatches the email, recording state in history.
func (c *HistoryClient) SendTemplate(ctx context.Context, req SendTemplateRequest) (*EmailRecordProto, error) {
	if strings.TrimSpace(req.TemplateID) == "" {
		return nil, ErrEmptyTemplateID
	}
	if err := req.Recipient.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidEmailAddress, err)
	}

	tmpl, err := GetTemplate(req.TemplateID)
	if err != nil {
		return nil, err
	}

	subject, html, text, err := tmpl.Render(req.Data)
	if err != nil {
		return nil, err
	}

	fromAddr := tmpl.From()
	if req.From != nil && req.From.Email != "" {
		fromAddr = *req.From
	}

	msg := &Message{
		From:        fromAddr,
		To:          []Address{req.Recipient},
		CC:          req.CC,
		BCC:         req.BCC,
		ReplyTo:     req.ReplyTo,
		Subject:     subject,
		HTML:        html,
		Text:        text,
		Attachments: req.Attachments,
		Headers:     req.Headers,
	}

	return c.SendMessage(ctx, SendMessageRequest{
		Message:          msg,
		TemplateID:       req.TemplateID,
		DeduplicationKey: req.DeduplicationKey,
		Sender:           req.Sender,
		ClaimTimeout:     req.ClaimTimeout,
	})
}

// SendMessageRequest defines parameters for synchronously dispatching an email message with history tracking.
type SendMessageRequest struct {
	Message          *Message
	TemplateID       string
	DeduplicationKey string
	Sender           Sender
	ClaimTimeout     time.Duration
}

// SendMessage synchronously dispatches an email message, coordinating claim and completion state in docstore.
func (c *HistoryClient) SendMessage(ctx context.Context, req SendMessageRequest) (*EmailRecordProto, error) {
	if req.Message == nil {
		return nil, ErrNilMessage
	}

	if err := req.Message.Validate(); err != nil {
		return nil, err
	}

	sender := req.Sender
	if sender == nil {
		var err error
		sender, err = DefaultSender()
		if err != nil {
			return nil, err
		}
	}

	claimTimeout := req.ClaimTimeout
	if claimTimeout <= 0 {
		claimTimeout = DefaultClaimTimeout
	}

	dedupKey := strings.TrimSpace(req.DeduplicationKey)

	recordID, err := goutils.RandomID()
	if err != nil {
		return nil, fmt.Errorf("email: failed to generate record ID: %w", err)
	}

	// Step 1: Claim the send synchronously in docstore (atomic OCC across pods)
	claimErr := c.Update(ctx, func(m proto.Message) error {
		doc := m.(*EmailHistoryProto)
		nowMs := time.Now().UnixMilli()

		if dedupKey != "" {
			for _, rec := range doc.Emails {
				if rec.DeduplicationKey == dedupKey {
					switch rec.Status {
					case EmailStatus_EMAIL_STATUS_SENT:
						return fmt.Errorf("%w: key %q was sent at %d", ErrAlreadySent, dedupKey, rec.SentAtUnixMs)
					case EmailStatus_EMAIL_STATUS_CLAIMED:
						if nowMs-rec.ClaimedAtUnixMs < claimTimeout.Milliseconds() {
							return fmt.Errorf("%w: key %q claimed at %d", ErrCurrentlyClaimed, dedupKey, rec.ClaimedAtUnixMs)
						}
						// Claim timed out; allow reclaim
						rec.Status = EmailStatus_EMAIL_STATUS_CLAIMED
						rec.ClaimedAtUnixMs = nowMs
						rec.Subject = req.Message.Subject
						rec.TemplateId = req.TemplateID
						rec.RecipientEmail = req.Message.To[0].Email
						rec.RecipientName = req.Message.To[0].Name
						rec.LastError = ""
						recordID = rec.Id
						return nil
					case EmailStatus_EMAIL_STATUS_FAILED:
						// Previous send failed; retry send under new claim
						rec.Status = EmailStatus_EMAIL_STATUS_CLAIMED
						rec.ClaimedAtUnixMs = nowMs
						rec.Subject = req.Message.Subject
						rec.TemplateId = req.TemplateID
						rec.RecipientEmail = req.Message.To[0].Email
						rec.RecipientName = req.Message.To[0].Name
						rec.LastError = ""
						recordID = rec.Id
						return nil
					}
				}
			}
		}

		pruneHistory(doc, DefaultHistoryMaxEntries)

		nowMs = time.Now().UnixMilli()
		newRec := &EmailRecordProto{
			Id:               recordID,
			DeduplicationKey: dedupKey,
			TemplateId:       req.TemplateID,
			RecipientEmail:   req.Message.To[0].Email,
			RecipientName:    req.Message.To[0].Name,
			Subject:          req.Message.Subject,
			Status:           EmailStatus_EMAIL_STATUS_CLAIMED,
			ClaimedAtUnixMs:  nowMs,
		}
		doc.Emails = append(doc.Emails, newRec)
		return nil
	})

	if claimErr != nil {
		if errors.Is(claimErr, ErrAlreadySent) || errors.Is(claimErr, ErrCurrentlyClaimed) {
			slog.DebugContext(ctx, "email send skipped (already sent or claimed)",
				"tenant_id", c.tenantID,
				"dedup_key", dedupKey,
				"template_id", req.TemplateID,
				"recipient", req.Message.To[0].Email,
				"subject", req.Message.Subject,
			)
		} else {
			slog.WarnContext(ctx, "failed to claim email send",
				"tenant_id", c.tenantID,
				"dedup_key", dedupKey,
				"template_id", req.TemplateID,
				"recipient", req.Message.To[0].Email,
				"error", claimErr,
			)
		}
		return nil, claimErr
	}

	// Step 2: Synchronously send the email
	sendErr := sender.Send(ctx, req.Message)

	// Step 3: Record outcome synchronously in docstore
	var finalRecord *EmailRecordProto
	outcomeErr := c.Update(ctx, func(m proto.Message) error {
		doc := m.(*EmailHistoryProto)
		nowMs := time.Now().UnixMilli()

		for _, rec := range doc.Emails {
			if rec.Id == recordID {
				if sendErr == nil {
					rec.Status = EmailStatus_EMAIL_STATUS_SENT
					rec.SentAtUnixMs = nowMs
					rec.LastError = ""
				} else {
					rec.Status = EmailStatus_EMAIL_STATUS_FAILED
					rec.LastError = sendErr.Error()
				}
				finalRecord = proto.Clone(rec).(*EmailRecordProto)
				return nil
			}
		}
		return ErrRecordNotFound
	})

	if sendErr != nil {
		slog.WarnContext(ctx, "failed to send email",
			"tenant_id", c.tenantID,
			"template_id", req.TemplateID,
			"recipient", req.Message.To[0].Email,
			"subject", req.Message.Subject,
			"record_id", recordID,
			"error", sendErr,
		)
		return finalRecord, fmt.Errorf("email: send failed: %w", sendErr)
	}
	if outcomeErr != nil {
		slog.WarnContext(ctx, "email sent but failed to update history status",
			"tenant_id", c.tenantID,
			"template_id", req.TemplateID,
			"recipient", req.Message.To[0].Email,
			"subject", req.Message.Subject,
			"record_id", recordID,
			"error", outcomeErr,
		)
		return finalRecord, fmt.Errorf("email: send succeeded but failed to update history status: %w", outcomeErr)
	}

	slog.InfoContext(ctx, "sent email",
		"tenant_id", c.tenantID,
		"template_id", req.TemplateID,
		"recipient", req.Message.To[0].Email,
		"subject", req.Message.Subject,
		"record_id", recordID,
	)

	return finalRecord, nil
}

func pruneHistory(doc *EmailHistoryProto, maxEntries int) {
	if len(doc.Emails) < maxEntries {
		return
	}
	kept := make([]*EmailRecordProto, 0, len(doc.Emails))
	excess := len(doc.Emails) - (maxEntries - 1)
	for _, rec := range doc.Emails {
		if excess > 0 && (rec.Status == EmailStatus_EMAIL_STATUS_SENT || rec.Status == EmailStatus_EMAIL_STATUS_FAILED) {
			excess--
			continue
		}
		kept = append(kept, rec)
	}
	doc.Emails = kept
}
