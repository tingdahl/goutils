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

func TestUsersClient_HeldPointerFreshness(t *testing.T) {
	uc, mockStore := setupTestUsersClient(t)
	ctx := context.Background()

	if _, err := uc.AddUserWithIdentity(ctx, "initial@example.com", "u-1", "Initial User", "google", "sub-1"); err != nil {
		t.Fatalf("failed to add initial user: %v", err)
	}

	// Simulate an out-of-band write to storage (e.g. another pod or admin tool adding u-2).
	externalDoc := &UsersProto{
		SchemaMinorVersion: SchemaMinor,
		Users: []*UserProtoRow{
			{Id: "u-1", Email: "initial@example.com", Name: "Initial User", Identities: map[string]string{"google": "sub-1"}},
			{Id: "u-2", Email: "external@example.com", Name: "External User", Identities: map[string]string{"google": "sub-2"}},
		},
	}
	raw, err := proto.Marshal(externalDoc)
	if err != nil {
		t.Fatalf("failed to marshal external doc: %v", err)
	}
	if _, err := mockStore.WriteObject(ctx, DefaultUsersDocumentName, raw); err != nil {
		t.Fatalf("failed to write external doc: %v", err)
	}

	// Wait for the 10ms revision check interval configured in setupTestUsersClient to elapse.
	time.Sleep(20 * time.Millisecond)

	// Calling GetUserByEmail directly on the held uc pointer (without calling GetUsersClient)
	// must automatically check storage and reload the document.
	got := uc.GetUserByEmail("external@example.com")
	if got == nil || got.Id != "u-2" {
		t.Fatalf("expected held UsersClient pointer to auto-reload external user u-2, got: %+v", got)
	}
}

func TestUsersClient_VerifyOnMiss_MultiPod(t *testing.T) {
	ResetForTesting()
	mockStore := storage.NewMockStorageClient()
	// Configure with standard 5-minute revision check interval so normal interval checks do NOT fire.
	err := Init(mockStore, docstore.WithRevisionPolicy(docstore.CheckInterval(5*time.Minute)))
	if err != nil {
		t.Fatalf("failed to init users repo: %v", err)
	}

	ctx := context.Background()

	// Pod 1 creates and initializes the document with an initial user.
	pod1Uc, err := GetUsersClient(ctx)
	if err != nil {
		t.Fatalf("failed to get pod1 users client: %v", err)
	}
	if _, err := pod1Uc.AddUserWithIdentity(ctx, "pod1@example.com", "u-1", "Pod1 User", "google", "sub-1"); err != nil {
		t.Fatalf("pod1 failed to add user: %v", err)
	}

	// Pod 2 loads the document into its own distinct client instance.
	pod2Doc := &UsersClient{}
	pod2Doc.Document = docstore.Document{
		Client:               pod2Doc,
		Storage:              mockStore,
		ObjectName:           DefaultUsersDocumentName,
		ProtoMsg:             &pod2Doc.users,
		SupportedSchemaMinor: SchemaMinor,
	}
	if err := pod2Doc.DoLoad(); err != nil {
		t.Fatalf("pod2 failed to load document: %v", err)
	}
	pod2Doc.SetRevisionPolicy(docstore.CheckInterval(5 * time.Minute))

	// Verify Pod 2 has u-1 in memory.
	if u := pod2Doc.GetUserByID("u-1"); u == nil {
		t.Fatalf("pod2 expected to have u-1 in memory")
	}

	// Pod 1 writes a new user (simulating cross-replica write in k8s).
	if _, err := pod1Uc.AddUserWithIdentity(ctx, "pod2-new@example.com", "u-2", "Pod2 New", "google", "sub-2"); err != nil {
		t.Fatalf("pod1 failed to add u-2: %v", err)
	}

	// Pod 2 receives a request for u-2 IMMEDIATELY (0ms wait, well within 5m interval).
	// On cache miss, Pod 2 must verify with storage, reload, and return u-2.
	byID := pod2Doc.GetUserByID("u-2")
	if byID == nil || byID.Email != "pod2-new@example.com" {
		t.Fatalf("pod2 failed to find u-2 by ID via verify-on-miss: %+v", byID)
	}

	// Verify lookups by email and identity also work seamlessly.
	byEmail := pod2Doc.GetUserByEmail("pod2-new@example.com")
	if byEmail == nil || byEmail.Id != "u-2" {
		t.Fatalf("pod2 failed to find u-2 by email via verify-on-miss: %+v", byEmail)
	}

	byIdent := pod2Doc.GetUserByIdentity("google", "sub-2")
	if byIdent == nil || byIdent.Id != "u-2" {
		t.Fatalf("pod2 failed to find u-2 by identity via verify-on-miss: %+v", byIdent)
	}

	// Non-existent user returns nil cleanly without error.
	if nonExistent := pod2Doc.GetUserByID("u-does-not-exist"); nonExistent != nil {
		t.Fatalf("expected nil for non-existent user, got: %+v", nonExistent)
	}
}
