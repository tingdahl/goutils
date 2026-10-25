package users

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tingdahl/goutils/auth"
	"github.com/tingdahl/goutils/storage"
	"github.com/tingdahl/goutils/storage/docstore"
	"google.golang.org/protobuf/proto"
)

func setupTestUsersClient(t *testing.T) (*UsersClient, *storage.MockStorageClient) {
	ResetForTesting()
	mockStore := storage.NewMockStorageClient()
	err := Init(mockStore, docstore.WithRevisionPolicy(docstore.CheckInterval(10*time.Millisecond)))
	if err != nil {
		t.Fatalf("failed to init users repo: %v", err)
	}

	uc, err := GetUsersClient(context.Background())
	if err != nil {
		t.Fatalf("failed to get users client: %v", err)
	}
	return uc, mockStore
}

func TestUsersClient_CRUD(t *testing.T) {
	uc, _ := setupTestUsersClient(t)
	ctx := context.Background()

	// 1. Add user
	u, err := uc.AddUserWithIdentity(ctx, "test@example.com", "u-1", "Test User", "google", "sub-123")
	if err != nil {
		t.Fatalf("failed to add user: %v", err)
	}
	if u.Id != "u-1" || u.Email != "test@example.com" || u.Name != "Test User" {
		t.Errorf("unexpected user: %+v", u)
	}
	if !u.HasLinkedIdentity() {
		t.Error("expected HasLinkedIdentity to be true")
	}

	// 2. Duplicate user prevention
	_, err = uc.AddUser(ctx, "test@example.com", "u-2", "Another")
	if !errors.Is(err, ErrUserAlreadyExists) {
		t.Errorf("expected ErrUserAlreadyExists for email match, got: %v", err)
	}
	_, err = uc.AddUser(ctx, "diff@example.com", "u-1", "Another")
	if !errors.Is(err, ErrUserAlreadyExists) {
		t.Errorf("expected ErrUserAlreadyExists for ID match, got: %v", err)
	}

	// 3. Lookups
	byID := uc.GetUserByID("u-1")
	if byID == nil || byID.Email != "test@example.com" {
		t.Errorf("failed to get user by ID")
	}
	byEmail := uc.GetUserByEmail("TEST@example.com") // Case-insensitive
	if byEmail == nil || byEmail.Id != "u-1" {
		t.Errorf("failed to get user by email case-insensitively")
	}
	byIdent := uc.GetUserByIdentity("google", "sub-123")
	if byIdent == nil || byIdent.Id != "u-1" {
		t.Errorf("failed to get user by identity")
	}

	// 4. Update Name and Email
	if err := uc.UpdateUserName(ctx, "u-1", "Updated Name"); err != nil {
		t.Fatalf("failed to update name: %v", err)
	}
	if uc.GetUserByID("u-1").Name != "Updated Name" {
		t.Errorf("expected name updated")
	}
	if err := uc.UpdateUserEmail(ctx, "u-1", "new@example.com"); err != nil {
		t.Fatalf("failed to update email: %v", err)
	}
	if uc.GetUserByID("u-1").Email != "new@example.com" {
		t.Errorf("expected email updated")
	}

	// 5. Tenant Roles
	if err := uc.SetTenantRole(ctx, "u-1", 100, 42); err != nil {
		t.Fatalf("failed to set role: %v", err)
	}
	u = uc.GetUserByID("u-1")
	if !u.HasTenantRole(100, 42) {
		t.Errorf("expected user to have role 42 on tenant 100")
	}
	r, ok := u.GetTenantRole(100)
	if !ok || r != 42 {
		t.Errorf("expected GetTenantRole(100) to return 42, true")
	}
	tenantUsers := uc.ListUsersForTenant(100)
	if len(tenantUsers) != 1 || tenantUsers[0].Id != "u-1" {
		t.Errorf("expected 1 tenant user in list")
	}

	// Remove tenant role
	if err := uc.RemoveTenantRole(ctx, "u-1", 100); err != nil {
		t.Fatalf("failed to remove role: %v", err)
	}
	if uc.GetUserByID("u-1").HasTenantRole(100, 42) {
		t.Errorf("expected role removed")
	}

	// 6. Terms Acceptance
	updated, err := uc.SetTCAccepted(ctx, "u-1", "2026-10-25")
	if err != nil {
		t.Fatalf("failed to set TC: %v", err)
	}
	if updated.TcAcceptedVersion != "2026-10-25" || updated.TcAcceptedAtUnixMs == 0 {
		t.Errorf("unexpected TC status: %+v", updated)
	}

	// 7. Trial Claim
	claimed, err := uc.ClaimTrial(ctx, "u-1")
	if err != nil || !claimed {
		t.Fatalf("failed to claim trial: %v, claimed=%v", err, claimed)
	}
	if !uc.GetUserByID("u-1").TrialClaimed {
		t.Errorf("expected trial claimed")
	}
	claimedAgain, err := uc.ClaimTrial(ctx, "u-1")
	if err != nil || claimedAgain {
		t.Errorf("expected claimedAgain=false, got %v, err=%v", claimedAgain, err)
	}

	// 8. Delete User
	if err := uc.DeleteUser(ctx, "u-1"); err != nil {
		t.Fatalf("failed to delete user: %v", err)
	}
	if uc.GetUserByID("u-1") != nil {
		t.Errorf("expected user deleted")
	}
}

func TestIdentityResolver(t *testing.T) {
	uc, _ := setupTestUsersClient(t)
	ctx := context.Background()

	allowSignup := true
	resolver := &IdentityResolver{
		UsersClient:       uc,
		AllowPublicSignup: &allowSignup,
		IsSiteAdminFn: func(email string) bool {
			return email == "admin@example.com"
		},
	}

	// 1. New user registration via OIDC
	identity := auth.UserIdentity{
		ProviderID:    "google",
		Subject:       "sub-g-1",
		Email:         "newuser@example.com",
		EmailVerified: true,
		Name:          "New User",
	}
	ctxUser, err := resolver.ResolveIdentity(ctx, identity)
	if err != nil {
		t.Fatalf("failed to resolve new user: %v", err)
	}
	u, ok := GetUser(ctxUser)
	if !ok || u == nil {
		t.Fatalf("expected user in context")
	}
	if u.Email != "newuser@example.com" || u.Identities["google"] != "sub-g-1" {
		t.Errorf("unexpected user created: %+v", u)
	}

	// 2. Resolve existing user by identity
	ctxExisting, err := resolver.ResolveIdentity(ctx, identity)
	if err != nil {
		t.Fatalf("failed to resolve existing user: %v", err)
	}
	uExisting, _ := GetUser(ctxExisting)
	if uExisting.Id != u.Id {
		t.Errorf("expected same user ID")
	}

	// 3. Link second provider by verified email
	identityMicrosoft := auth.UserIdentity{
		ProviderID:    "microsoft",
		Subject:       "sub-ms-1",
		Email:         "newuser@example.com",
		EmailVerified: true,
		Name:          "New User MS",
	}
	ctxLinked, err := resolver.ResolveIdentity(ctx, identityMicrosoft)
	if err != nil {
		t.Fatalf("failed to link provider: %v", err)
	}
	uLinked, _ := GetUser(ctxLinked)
	if uLinked.Identities["microsoft"] != "sub-ms-1" || uLinked.Identities["google"] != "sub-g-1" {
		t.Errorf("expected both identities linked: %+v", uLinked.Identities)
	}

	// 4. Reject identity conflict (same provider, different subject, same email)
	identityConflict := auth.UserIdentity{
		ProviderID:    "google",
		Subject:       "sub-g-DIFFERENT",
		Email:         "newuser@example.com",
		EmailVerified: true,
	}
	_, err = resolver.ResolveIdentity(ctx, identityConflict)
	if !errors.Is(err, ErrIdentityConflict) {
		t.Errorf("expected ErrIdentityConflict, got: %v", err)
	}

	// 5. Reject unverified email
	identityUnverified := auth.UserIdentity{
		ProviderID:    "google",
		Subject:       "sub-g-unverified",
		Email:         "unverified@example.com",
		EmailVerified: false,
	}
	_, err = resolver.ResolveIdentity(ctx, identityUnverified)
	if !errors.Is(err, ErrEmailNotVerified) {
		t.Errorf("expected ErrEmailNotVerified, got: %v", err)
	}

	// 6. Session user resolution
	ctxSession, err := resolver.ResolveSessionUser(ctx, &auth.UserSessionClaims{
		Sub:      u.Id,
		Provider: "google",
	})
	if err != nil {
		t.Fatalf("failed to resolve session user: %v", err)
	}
	uSession, ok := GetUser(ctxSession)
	if !ok || uSession.Id != u.Id {
		t.Errorf("expected session user resolved")
	}
}

func TestUsersClient_DropLegacyInvitePlaceholders(t *testing.T) {
	doc := &UsersProto{
		SchemaMinorVersion: 1,
		Users: []*UserProtoRow{
			{Id: "u-active", Email: "active@example.com", Identities: map[string]string{"google": "sub-1"}},
			{Id: "u-invite-placeholder", Email: "invited@example.com", Identities: nil},
		},
	}
	data, _ := proto.Marshal(doc)

	uc := &UsersClient{}
	if err := uc.ReadFromProto(data); err != nil {
		t.Fatalf("failed to read proto: %v", err)
	}
	if len(uc.users.Users) != 1 || uc.users.Users[0].Id != "u-active" {
		t.Errorf("expected legacy invite placeholder to be dropped, got %d users", len(uc.users.Users))
	}
}
