package users

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tingdahl/goutils/audit"
	"github.com/tingdahl/goutils/storage"
	"github.com/tingdahl/goutils/storage/docstore"
	"google.golang.org/protobuf/proto"
)

const (
	// SchemaMinor is the current schema minor version supported by this code.
	SchemaMinor int32 = 2

	// DefaultUsersDocumentName is the default S3 object key with major version for the users document.
	DefaultUsersDocumentName = "users.v1.pb.zst"

	// UsersRevisionCheckInterval is the default period before checking storage for updates.
	UsersRevisionCheckInterval = 60 * time.Second
)

var (
	ErrUserNotFound          = errors.New("user not found")
	ErrUserAlreadyExists     = errors.New("user already exists")
	ErrIdentityAlreadyLinked = errors.New("a different identity is already linked for this provider")

	usersInitOnce sync.Once
	usersRepo     *docstore.Repository[*UsersClient]
	usersStore    storage.StorageClient
)

func cloneUser(row *UserProtoRow) *UserProtoRow {
	if row == nil {
		return nil
	}
	return proto.Clone(row).(*UserProtoRow)
}

// UsersClient manages users persistence backed by docstore.Document.
type UsersClient struct {
	docstore.Document
	users UsersProto
}

func usersClientFactory(ctx context.Context, objectPath string) (*UsersClient, error) {
	if usersStore == nil {
		return nil, errors.New("storage client cannot be nil")
	}
	if objectPath == "" {
		return nil, errors.New("object path cannot be empty")
	}

	uc := &UsersClient{}
	uc.Document = docstore.Document{
		Client:               uc,
		Storage:              usersStore,
		ObjectName:           objectPath,
		ProtoMsg:             &uc.users,
		SupportedSchemaMinor: SchemaMinor,
	}

	if err := uc.DoLoad(); err != nil {
		return nil, fmt.Errorf("failed to load users document: %w", err)
	}

	return uc, nil
}

// Init initializes the users repository for the given storage client.
func Init(store storage.StorageClient, opts ...docstore.RepositoryOption) error {
	if store == nil {
		return errors.New("storage client cannot be nil")
	}

	usersInitOnce.Do(func() {
		usersStore = store
		defaultOpts := []docstore.RepositoryOption{
			docstore.WithRevisionPolicy(docstore.CheckInterval(UsersRevisionCheckInterval)),
		}
		allOpts := append(defaultOpts, opts...)
		usersRepo = docstore.NewRepository(usersClientFactory, allOpts...)
	})
	return nil
}

// GetUsersClient returns the cached UsersClient from the repository using DefaultUsersDocumentName.
func GetUsersClient(ctx context.Context, opts ...docstore.GetOption) (*UsersClient, error) {
	return GetUsersClientForDocument(ctx, DefaultUsersDocumentName, opts...)
}

// GetUsersClientForDocument returns the cached UsersClient for a specific document name.
func GetUsersClientForDocument(ctx context.Context, docName string, opts ...docstore.GetOption) (*UsersClient, error) {
	if usersRepo == nil {
		return nil, errors.New("users package not initialized: call users.Init() first")
	}
	return usersRepo.Get(ctx, docName, opts...)
}

// ResetForTesting resets the package-level repository state for test isolation.
func ResetForTesting() {
	usersInitOnce = sync.Once{}
	usersRepo = nil
	usersStore = nil
}

// ReadFromProto decodes raw protobuf bytes into the in-memory users document.
func (u *UsersClient) ReadFromProto(data []byte) error {
	if len(data) == 0 {
		u.users = UsersProto{}
		return nil
	}
	if err := proto.Unmarshal(data, &u.users); err != nil {
		return err
	}
	if u.users.SchemaMinorVersion < 2 {
		dropLegacyInvitePlaceholders(&u.users)
	}
	return nil
}

func dropLegacyInvitePlaceholders(doc *UsersProto) {
	kept := doc.Users[:0]
	for _, row := range doc.Users {
		if len(row.Identities) > 0 {
			kept = append(kept, row)
		}
	}
	doc.Users = kept
}

// GetSchemaMinorVersion returns the minor schema version from the loaded document.
func (u *UsersClient) GetSchemaMinorVersion() int32 {
	return u.users.SchemaMinorVersion
}

// StampVersion updates the schema version and last modified commit on write.
func (u *UsersClient) StampVersion(msg proto.Message, schemaMinor int32, commit string) {
	doc, ok := msg.(*UsersProto)
	if ok {
		doc.SchemaMinorVersion = schemaMinor
		doc.LastModifiedByCommit = commit
	}
}

// GetUserByID looks up a user by user ID under a read lock.
func (u *UsersClient) GetUserByID(id string) *UserProtoRow {
	u.Rwlock.RLock()
	defer u.Rwlock.RUnlock()

	for _, row := range u.users.Users {
		if row.Id == id {
			return cloneUser(row)
		}
	}
	return nil
}

// GetUserByEmail looks up a user by email address (case-insensitive) under a read lock.
func (u *UsersClient) GetUserByEmail(email string) *UserProtoRow {
	u.Rwlock.RLock()
	defer u.Rwlock.RUnlock()

	for _, row := range u.users.Users {
		if strings.EqualFold(row.Email, email) {
			return cloneUser(row)
		}
	}
	return nil
}

// GetUserByIdentity looks up a user by OIDC provider and subject ID under a read lock.
func (u *UsersClient) GetUserByIdentity(providerID, subjectID string) *UserProtoRow {
	u.Rwlock.RLock()
	defer u.Rwlock.RUnlock()

	for _, row := range u.users.Users {
		if row.Identities != nil && row.Identities[providerID] == subjectID {
			return cloneUser(row)
		}
	}
	return nil
}

// ListUsersForTenant returns all users that have a role in the given tenant.
func (u *UsersClient) ListUsersForTenant(tenantID int64) []*UserProtoRow {
	u.Rwlock.RLock()
	defer u.Rwlock.RUnlock()

	var result []*UserProtoRow
	for _, row := range u.users.Users {
		if row.TenantRoles != nil {
			if _, ok := row.TenantRoles[tenantID]; ok {
				result = append(result, cloneUser(row))
			}
		}
	}
	return result
}

// ListAllUsers returns copies of all users in the document.
func (u *UsersClient) ListAllUsers() []*UserProtoRow {
	u.Rwlock.RLock()
	defer u.Rwlock.RUnlock()

	result := make([]*UserProtoRow, len(u.users.Users))
	for i, row := range u.users.Users {
		result[i] = cloneUser(row)
	}
	return result
}

// logAudit writes an audit entry if storage is available, ignoring failures so primary mutations succeed.
func (u *UsersClient) logAudit(ctx context.Context, tenantID int64, action, humanSummary string, details map[string]string) {
	if u.Storage == nil {
		return
	}
	prefix := "audit/users"
	if tenantID != 0 {
		prefix = fmt.Sprintf("tenants/%d/audit", tenantID)
	}
	_ = audit.Log(ctx, u.Storage, prefix, audit.AuditEntry{
		Module:           "users",
		Action:           action,
		HumanSummary:     humanSummary,
		TechnicalDetails: details,
	})
}

// AddUser adds a new user without linked OIDC identities.
func (u *UsersClient) AddUser(ctx context.Context, email, id, name string) (*UserProtoRow, error) {
	return u.AddUserWithIdentity(ctx, email, id, name, "", "")
}

// AddUserWithIdentity adds a new user and links an initial OIDC identity.
func (u *UsersClient) AddUserWithIdentity(ctx context.Context, email, id, name, providerID, subjectID string) (*UserProtoRow, error) {
	now := time.Now().UnixMilli()

	var createdUser *UserProtoRow
	err := u.Update(ctx, func(msg proto.Message) error {
		doc := msg.(*UsersProto)
		for _, row := range doc.Users {
			if row.Id == id {
				return ErrUserAlreadyExists
			}
			if strings.EqualFold(row.Email, email) {
				return ErrUserAlreadyExists
			}
		}

		createdUser = &UserProtoRow{
			Id:               id,
			Email:            email,
			Name:             name,
			CreatedAtUnixMs:  now,
			UpdatedAtUnixMs:  now,
			TenantRoles:      make(map[int64]int32),
			Identities:       make(map[string]string),
		}
		if providerID != "" && subjectID != "" {
			createdUser.Identities[providerID] = subjectID
		}

		doc.Users = append(doc.Users, createdUser)
		return nil
	})
	if err != nil {
		return nil, err
	}

	u.logAudit(ctx, 0, "USER_CREATED", fmt.Sprintf("User %s (%s) created", name, email), map[string]string{
		"user_id":  id,
		"email":    email,
		"name":     name,
		"provider": providerID,
	})

	return cloneUser(createdUser), nil
}

// LinkUserIdentity links a new OIDC provider and subject ID to an existing user.
func (u *UsersClient) LinkUserIdentity(ctx context.Context, userID, providerID, subjectID string) error {
	now := time.Now().UnixMilli()
	var targetEmail string

	err := u.Update(ctx, func(msg proto.Message) error {
		doc := msg.(*UsersProto)
		for _, row := range doc.Users {
			if row.Id == userID {
				targetEmail = row.Email
				if row.Identities == nil {
					row.Identities = make(map[string]string)
				}
				if existing, ok := row.Identities[providerID]; ok && existing != subjectID {
					return ErrIdentityAlreadyLinked
				}
				row.Identities[providerID] = subjectID
				row.UpdatedAtUnixMs = now
				return nil
			}
		}
		return ErrUserNotFound
	})
	if err != nil {
		return err
	}

	u.logAudit(ctx, 0, "USER_IDENTITY_LINKED", fmt.Sprintf("Linked provider %s to user %s", providerID, targetEmail), map[string]string{
		"user_id":  userID,
		"provider": providerID,
		"subject":  subjectID,
	})

	return nil
}

// UpdateUserName updates the display name of a user.
func (u *UsersClient) UpdateUserName(ctx context.Context, userID, name string) error {
	now := time.Now().UnixMilli()
	return u.Update(ctx, func(msg proto.Message) error {
		doc := msg.(*UsersProto)
		for _, row := range doc.Users {
			if row.Id == userID {
				row.Name = name
				row.UpdatedAtUnixMs = now
				return nil
			}
		}
		return ErrUserNotFound
	})
}

// UpdateUserEmail updates the email of a user.
func (u *UsersClient) UpdateUserEmail(ctx context.Context, userID, newEmail string) error {
	now := time.Now().UnixMilli()
	return u.Update(ctx, func(msg proto.Message) error {
		doc := msg.(*UsersProto)
		for _, row := range doc.Users {
			if row.Id == userID {
				row.Email = newEmail
				row.UpdatedAtUnixMs = now
				return nil
			}
		}
		return ErrUserNotFound
	})
}

// SetTenantRole sets the role for a user in a tenant.
// SetUserTenantRole assigns or updates a user's role in a tenant.
func (u *UsersClient) SetUserTenantRole(ctx context.Context, userID string, tenantID int64, role int32) error {
	return u.SetTenantRole(ctx, userID, tenantID, role)
}

func (u *UsersClient) SetTenantRole(ctx context.Context, userID string, tenantID int64, role int32) error {
	now := time.Now().UnixMilli()
	var targetEmail string

	err := u.Update(ctx, func(msg proto.Message) error {
		doc := msg.(*UsersProto)
		for _, row := range doc.Users {
			if row.Id == userID {
				targetEmail = row.Email
				if row.TenantRoles == nil {
					row.TenantRoles = make(map[int64]int32)
				}
				row.TenantRoles[tenantID] = role
				row.UpdatedAtUnixMs = now
				return nil
			}
		}
		return ErrUserNotFound
	})
	if err != nil {
		return err
	}

	u.logAudit(ctx, tenantID, "USER_ROLE_SET", fmt.Sprintf("Set role %d for user %s on tenant %d", role, targetEmail, tenantID), map[string]string{
		"user_id":   userID,
		"tenant_id": fmt.Sprintf("%d", tenantID),
		"role":      fmt.Sprintf("%d", role),
	})

	return nil
}

// RemoveTenantRole removes a user's role in a tenant.
func (u *UsersClient) RemoveTenantRole(ctx context.Context, userID string, tenantID int64) error {
	now := time.Now().UnixMilli()
	var targetEmail string

	err := u.Update(ctx, func(msg proto.Message) error {
		doc := msg.(*UsersProto)
		for _, row := range doc.Users {
			if row.Id == userID {
				targetEmail = row.Email
				if row.TenantRoles != nil {
					delete(row.TenantRoles, tenantID)
				}
				row.UpdatedAtUnixMs = now
				return nil
			}
		}
		return ErrUserNotFound
	})
	if err != nil {
		return err
	}

	u.logAudit(ctx, tenantID, "USER_ROLE_REMOVED", fmt.Sprintf("Removed role for user %s on tenant %d", targetEmail, tenantID), map[string]string{
		"user_id":   userID,
		"tenant_id": fmt.Sprintf("%d", tenantID),
	})

	return nil
}

// SetTCAccepted records the version and timestamp of the Terms & Conditions accepted by the user.
func (u *UsersClient) SetTCAccepted(ctx context.Context, userID string, version string) (*UserProtoRow, error) {
	now := time.Now().UnixMilli()
	var updatedRow *UserProtoRow

	err := u.Update(ctx, func(msg proto.Message) error {
		doc := msg.(*UsersProto)
		for _, row := range doc.Users {
			if row.Id == userID {
				row.TcAcceptedVersion = version
				row.TcAcceptedAtUnixMs = now
				row.UpdatedAtUnixMs = now
				updatedRow = row
				return nil
			}
		}
		return ErrUserNotFound
	})
	if err != nil {
		return nil, err
	}

	u.logAudit(ctx, 0, "TERMS_ACCEPTED", fmt.Sprintf("Accepted legal terms version %s", version), map[string]string{
		"user_id": userID,
		"version": version,
	})

	return cloneUser(updatedRow), nil
}

// SetTrialClaimed marks that the user has claimed their free trial tenant.
func (u *UsersClient) SetTrialClaimed(ctx context.Context, userID string) error {
	now := time.Now().UnixMilli()
	return u.Update(ctx, func(msg proto.Message) error {
		doc := msg.(*UsersProto)
		for _, row := range doc.Users {
			if row.Id == userID {
				row.TrialClaimed = true
				row.UpdatedAtUnixMs = now
				return nil
			}
		}
		return ErrUserNotFound
	})
}

// ClaimTrial atomically marks the user's one-time free trial as claimed.
// It returns true only for the single caller that flipped the flag; concurrent or later callers get false.
func (u *UsersClient) ClaimTrial(ctx context.Context, userID string) (bool, error) {
	now := time.Now().UnixMilli()
	claimed := false
	err := u.Update(ctx, func(msg proto.Message) error {
		claimed = false
		doc := msg.(*UsersProto)
		for _, row := range doc.Users {
			if row.Id == userID {
				if row.TrialClaimed {
					return nil
				}
				row.TrialClaimed = true
				row.UpdatedAtUnixMs = now
				claimed = true
				return nil
			}
		}
		return ErrUserNotFound
	})
	if err != nil {
		return false, err
	}
	return claimed, nil
}

// RunMaintenance performs periodic maintenance tasks for users.
func (u *UsersClient) RunMaintenance(ctx context.Context) error {
	return nil
}

// DeleteUser deletes a user by ID.
func (u *UsersClient) DeleteUser(ctx context.Context, userID string) error {
	var targetEmail string
	err := u.Update(ctx, func(msg proto.Message) error {
		doc := msg.(*UsersProto)
		for i, row := range doc.Users {
			if row.Id == userID {
				targetEmail = row.Email
				doc.Users = append(doc.Users[:i], doc.Users[i+1:]...)
				return nil
			}
		}
		return ErrUserNotFound
	})
	if err != nil {
		return err
	}

	u.logAudit(ctx, 0, "USER_DELETED", fmt.Sprintf("User %s deleted", targetEmail), map[string]string{
		"user_id": userID,
		"email":   targetEmail,
	})

	return nil
}
