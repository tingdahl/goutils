//go:generate protoc --go_out=. --go_opt=paths=source_relative invitations.v1.proto

// Package invitations implements single-use organization/tenant invitation links and storage.
package invitations

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tingdahl/goutils/auth"
	"github.com/tingdahl/goutils/storage"
	"github.com/tingdahl/goutils/storage/docstore"
	"google.golang.org/protobuf/proto"
)

const (
	// DbObjectName is the storage object filename for the invitations document.
	DbObjectName = "invitations.v1.pb.zst"

	// SchemaMinor is the current minor schema version supported by this package.
	SchemaMinor int32 = 1

	// DefaultInvitationLifetime is how long a pending invitation stays valid (7 days).
	DefaultInvitationLifetime = 7 * 24 * time.Hour

	// DefaultMaxPendingInvitations caps the number of unexpired pending invitations per organization.
	DefaultMaxPendingInvitations = 50

	// URL query parameter names for invitations.
	ParamInvite        = "invite"
	ParamInviteResult  = "invite_result"
	ParamInviteTarget  = "invite_target"
	ParamInviteTenant  = "invite_tenant"
	ParamInviteCompany = "invite_company"

	// Standard invitation outcome result codes.
	InviteResultAccepted = "accepted"
	InviteResultUsed     = "used"
	InviteResultInvalid  = "invalid"
	InviteResultExpired  = "expired"
)

var (
	// ErrInvalidLink is returned when the link signature, type, or expiry is invalid.
	ErrInvalidLink = errors.New("invalid or expired invitation link")

	// ErrInvitationUsed is returned when the referenced invitation was accepted, revoked or expired.
	ErrInvitationUsed = errors.New("invitation not found or already used")

	// ErrAlreadyMember is returned when inviting an address that already belongs to an active member.
	ErrAlreadyMember = errors.New("user is already an active member of this organization")

	// ErrInvitationNotFound is returned when an invitation does not exist, expired, was revoked or already accepted.
	ErrInvitationNotFound = errors.New("invitation not found, expired or already used")

	// ErrTooManyInvitations is returned when the target already has max pending invitations.
	ErrTooManyInvitations = errors.New("too many pending invitations")
)

// InvitationView is a pending invitation as presented to organization administrators.
type InvitationView struct {
	ID              string `json:"id"`
	Email           string `json:"email"`
	CreatedByUserID string `json:"created_by_user_id"`
	CreatedAtUnixMs int64  `json:"created_at_unix_ms"`
	ExpiresAtUnixMs int64  `json:"expires_at_unix_ms"`
	TargetID        int64  `json:"target_id,omitempty"`
	Role            int32  `json:"role,omitempty"`
	InviteToken     string `json:"invite_token,omitempty"`
	InviteURL       string `json:"invite_url,omitempty"`
}

// InvitationInfo is the public description of an invitation link shown before sign-in.
type InvitationInfo struct {
	TargetID        int64  `json:"target_id"`
	TenantID        int64  `json:"tenant_id,omitempty"`
	TargetName      string `json:"target_name"`
	TenantName      string `json:"tenant_name,omitempty"`
	InvitedEmail    string `json:"invited_email"`
	ExpiresAtUnixMs int64  `json:"expires_at_unix_ms"`
}

// InvitationResult describes a successfully accepted invitation.
type InvitationResult struct {
	TargetID   int64  `json:"target_id"`
	TenantID   int64  `json:"tenant_id,omitempty"`
	TargetName string `json:"target_name"`
	TenantName string `json:"tenant_name,omitempty"`
}

// InvitationsClient manages persistence of an individual target's invitations document.
type InvitationsClient struct {
	docstore.Document
	targetID int64
	data     InvitationsProto
}

func (c *InvitationsClient) TargetID() int64 {
	return c.targetID
}

func (c *InvitationsClient) ReadFromProto(data []byte) error {
	proto.Reset(&c.data)
	if data == nil {
		return nil
	}
	return proto.Unmarshal(data, &c.data)
}

func (c *InvitationsClient) GetSchemaMinorVersion() int32 {
	return c.data.SchemaMinorVersion
}

func (c *InvitationsClient) StampVersion(msg proto.Message, schemaMinor int32, commit string) {
	doc := msg.(*InvitationsProto)
	doc.TargetId = c.targetID
	doc.SchemaMinorVersion = schemaMinor
	doc.LastModifiedByCommit = commit
}

// pruneExpiredInvitations drops expired invitations in place.
func pruneExpiredInvitations(doc *InvitationsProto, nowMs int64) {
	kept := doc.Invitations[:0]
	for _, inv := range doc.Invitations {
		if inv.ExpiresAtUnixMs > nowMs {
			kept = append(kept, inv)
		}
	}
	doc.Invitations = kept
}

// Create records a new pending invitation on the target document.
func (c *InvitationsClient) Create(ctx context.Context, email, createdByUserID string, role int32, lifetime time.Duration, maxPending ...int) (*InvitationProto, error) {
	if lifetime <= 0 {
		lifetime = DefaultInvitationLifetime
	}
	maxCap := DefaultMaxPendingInvitations
	if len(maxPending) > 0 && maxPending[0] > 0 {
		maxCap = maxPending[0]
	}

	now := time.Now()
	nowMs := now.UnixMilli()
	inv := &InvitationProto{
		Id:              uuid.NewString(),
		Email:           strings.TrimSpace(strings.ToLower(email)),
		CreatedByUserId: createdByUserID,
		CreatedAtUnixMs: nowMs,
		ExpiresAtUnixMs: now.Add(lifetime).UnixMilli(),
		TargetId:        c.targetID,
		Role:            role,
	}

	err := c.Update(ctx, func(msg proto.Message) error {
		doc := msg.(*InvitationsProto)
		doc.TargetId = c.targetID
		pruneExpiredInvitations(doc, nowMs)
		if len(doc.Invitations) >= maxCap {
			return ErrTooManyInvitations
		}
		doc.Invitations = append(doc.Invitations, inv)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return proto.Clone(inv).(*InvitationProto), nil
}

// List returns the unexpired pending invitations, oldest first.
func (c *InvitationsClient) List(ctx context.Context) ([]*InvitationProto, error) {
	c.Rwlock.RLock()
	defer c.Rwlock.RUnlock()

	nowMs := time.Now().UnixMilli()
	var result []*InvitationProto
	for _, inv := range c.data.Invitations {
		if inv.ExpiresAtUnixMs > nowMs {
			result = append(result, proto.Clone(inv).(*InvitationProto))
		}
	}
	if result == nil {
		result = []*InvitationProto{}
	}
	return result, nil
}

// Get returns an unexpired pending invitation by id, or ErrInvitationNotFound.
func (c *InvitationsClient) Get(ctx context.Context, invitationID string) (*InvitationProto, error) {
	if strings.TrimSpace(invitationID) == "" {
		return nil, ErrInvitationNotFound
	}
	list, err := c.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, inv := range list {
		if inv.Id == invitationID {
			return inv, nil
		}
	}
	return nil, ErrInvitationNotFound
}

// Revoke removes a pending invitation by ID.
func (c *InvitationsClient) Revoke(ctx context.Context, invitationID string) error {
	_, err := c.removeInvitation(ctx, invitationID)
	return err
}

// Consume atomically removes an unexpired pending invitation and returns it (single use).
func (c *InvitationsClient) Consume(ctx context.Context, invitationID string) (*InvitationProto, error) {
	return c.removeInvitation(ctx, invitationID)
}

func (c *InvitationsClient) removeInvitation(ctx context.Context, invitationID string) (*InvitationProto, error) {
	if strings.TrimSpace(invitationID) == "" {
		return nil, ErrInvitationNotFound
	}
	nowMs := time.Now().UnixMilli()
	var removed *InvitationProto

	err := c.Update(ctx, func(msg proto.Message) error {
		doc := msg.(*InvitationsProto)
		doc.TargetId = c.targetID
		pruneExpiredInvitations(doc, nowMs)
		for i, inv := range doc.Invitations {
			if inv.Id == invitationID {
				removed = proto.Clone(inv).(*InvitationProto)
				doc.Invitations = append(doc.Invitations[:i], doc.Invitations[i+1:]...)
				return nil
			}
		}
		return ErrInvitationNotFound
	})
	if err != nil {
		return nil, err
	}
	return removed, nil
}

// MintToken creates a signed invitation JWT for a stored invitation, expiring with it.
func (c *InvitationsClient) MintToken(tm *auth.InvitationTokenManager, inv *InvitationProto) (string, error) {
	if tm == nil {
		return "", errors.New("invitation token manager cannot be nil")
	}
	if inv == nil {
		return "", errors.New("invitation cannot be nil")
	}
	return tm.MintInvitationToken(inv.Id, c.targetID, time.UnixMilli(inv.ExpiresAtUnixMs))
}

var (
	invitationsInitOnce sync.Once
	invitationsRepo     *docstore.Repository[*InvitationsClient]
	invitationsStore    storage.StorageClient
	invitationsPrefix   string = "tenants"
)

func invitationsClientFactory(ctx context.Context, objectPath string) (*InvitationsClient, error) {
	if invitationsStore == nil {
		return nil, errors.New("storage client cannot be nil")
	}
	if objectPath == "" {
		return nil, errors.New("object path cannot be empty")
	}

	client := &InvitationsClient{}
	client.Document = docstore.Document{
		Client:               client,
		Storage:              invitationsStore,
		ObjectName:           objectPath,
		ProtoMsg:             &client.data,
		SupportedSchemaMinor: SchemaMinor,
	}

	if err := client.DoLoad(); err != nil {
		return nil, fmt.Errorf("failed to load invitations document: %w", err)
	}

	return client, nil
}

// Init initializes the package-level invitations repository.
func Init(store storage.StorageClient, prefix string, opts ...docstore.RepositoryOption) error {
	if store == nil {
		return errors.New("storage client cannot be nil")
	}

	invitationsInitOnce.Do(func() {
		if strings.TrimSpace(prefix) != "" {
			invitationsPrefix = strings.Trim(prefix, "/")
		}
		invitationsStore = store
		invitationsRepo = docstore.NewRepository(invitationsClientFactory, opts...)
	})
	return nil
}

// ResetForTesting resets the package-level repository state for test isolation.
func ResetForTesting() {
	invitationsInitOnce = sync.Once{}
	invitationsRepo = nil
	invitationsStore = nil
	invitationsPrefix = "tenants"
}

// TargetInvitationsPath returns the storage object path for a given target ID.
func TargetInvitationsPath(targetID int64) string {
	prefix := strings.Trim(invitationsPrefix, "/")
	if prefix == "" {
		return fmt.Sprintf("%d/%s", targetID, DbObjectName)
	}
	return fmt.Sprintf("%s/%d/%s", prefix, targetID, DbObjectName)
}

// GetInvitationsClient retrieves the cached InvitationsClient for the specified target ID.
func GetInvitationsClient(ctx context.Context, targetID int64) (*InvitationsClient, error) {
	if invitationsRepo == nil {
		return nil, errors.New("invitations package not initialized: call invitations.Init() first")
	}
	if targetID <= 0 {
		return nil, errors.New("target ID must be greater than zero")
	}

	client, err := invitationsRepo.Get(ctx, TargetInvitationsPath(targetID))
	if err != nil {
		return nil, err
	}
	client.targetID = targetID
	return client, nil
}

// StripInviteParams extracts the invite query token if present, and removes invite/tenant/company params.
func StripInviteParams(targetURL string) (inviteToken string, cleanURL string) {
	u, err := url.Parse(targetURL)
	if err != nil {
		return "", targetURL
	}
	q := u.Query()
	inviteToken = q.Get(ParamInvite)
	q.Del(ParamInvite)
	q.Del(ParamInviteResult)
	q.Del(ParamInviteTarget)
	q.Del(ParamInviteTenant)
	q.Del(ParamInviteCompany)
	q.Del("email")
	q.Del("tenant")
	q.Del("church")
	q.Del("company")
	u.RawQuery = q.Encode()
	return inviteToken, u.String()
}

// DecorateInviteResult appends the invitation outcome and target ID to the target URL.
func DecorateInviteResult(targetURL, result string, targetID int64) string {
	u, err := url.Parse(targetURL)
	if err != nil {
		return targetURL
	}
	q := u.Query()
	if result != "" {
		q.Set(ParamInviteResult, result)
	}
	if targetID > 0 {
		tidStr := strconv.FormatInt(targetID, 10)
		q.Set(ParamInviteTarget, tidStr)
		q.Set(ParamInviteTenant, tidStr)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// BuildInviteURL builds a full invite link for a given base URL, app path, and token.
func BuildInviteURL(baseURL, appPath, token string) string {
	base := strings.TrimRight(baseURL, "/")
	path := strings.TrimLeft(appPath, "/")
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return fmt.Sprintf("%s/%s%sinvite=%s", base, path, sep, url.QueryEscape(token))
}
