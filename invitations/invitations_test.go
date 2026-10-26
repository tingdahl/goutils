package invitations_test

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/tingdahl/goutils/auth"
	"github.com/tingdahl/goutils/invitations"
	"github.com/tingdahl/goutils/storage"
)

func setupTest(t *testing.T) storage.StorageClient {
	t.Helper()
	invitations.ResetForTesting()
	store := storage.NewMockStorageClient()
	if err := invitations.Init(store, "tenants"); err != nil {
		t.Fatalf("failed to init invitations: %v", err)
	}
	return store
}

func TestInvitations_InitValidation(t *testing.T) {
	invitations.ResetForTesting()
	if err := invitations.Init(nil, "tenants"); err == nil {
		t.Fatal("expected error with nil store")
	}

	ctx := context.Background()
	if _, err := invitations.GetInvitationsClient(ctx, 1001); err == nil {
		t.Fatal("expected error when uninitialized")
	}
}

func TestInvitations_Lifecycle(t *testing.T) {
	setupTest(t)
	ctx := context.Background()

	client, err := invitations.GetInvitationsClient(ctx, 1001)
	if err != nil {
		t.Fatalf("failed to get client: %v", err)
	}

	// 1. Initial list is empty
	list, err := client.List(ctx)
	if err != nil {
		t.Fatalf("failed to list: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty list, got %d", len(list))
	}

	// 2. Create invitation
	inv1, err := client.Create(ctx, "Alice@Example.COM", "user_admin_1", 0, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("failed to create invitation: %v", err)
	}
	if inv1.Email != "alice@example.com" {
		t.Errorf("expected normalized email, got %s", inv1.Email)
	}
	if inv1.TargetId != 1001 {
		t.Errorf("expected target ID 1001, got %d", inv1.TargetId)
	}

	// 3. Get invitation by ID
	fetched, err := client.Get(ctx, inv1.Id)
	if err != nil {
		t.Fatalf("failed to get invitation: %v", err)
	}
	if fetched.Email != "alice@example.com" {
		t.Errorf("expected email %s, got %s", inv1.Email, fetched.Email)
	}

	// 4. Create second invitation
	inv2, err := client.Create(ctx, "bob@example.com", "user_admin_1", 1, 24*time.Hour)
	if err != nil {
		t.Fatalf("failed to create second invitation: %v", err)
	}

	list, err = client.List(ctx)
	if err != nil {
		t.Fatalf("failed to list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 invitations, got %d", len(list))
	}

	// 5. Revoke first invitation
	if err := client.Revoke(ctx, inv1.Id); err != nil {
		t.Fatalf("failed to revoke: %v", err)
	}
	if _, err := client.Get(ctx, inv1.Id); !errors.Is(err, invitations.ErrInvitationNotFound) {
		t.Fatalf("expected ErrInvitationNotFound after revoke, got %v", err)
	}

	// 6. Consume second invitation (atomic single use)
	consumed, err := client.Consume(ctx, inv2.Id)
	if err != nil {
		t.Fatalf("failed to consume: %v", err)
	}
	if consumed.Id != inv2.Id {
		t.Errorf("expected consumed ID %s, got %s", inv2.Id, consumed.Id)
	}

	// Consuming again must fail
	if _, err := client.Consume(ctx, inv2.Id); !errors.Is(err, invitations.ErrInvitationNotFound) {
		t.Fatalf("expected ErrInvitationNotFound on second consume, got %v", err)
	}

	// Final list empty
	list, err = client.List(ctx)
	if err != nil {
		t.Fatalf("failed to list: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0 invitations remaining, got %d", len(list))
	}
}

func TestInvitations_ExpirationAndCap(t *testing.T) {
	setupTest(t)
	ctx := context.Background()

	client, err := invitations.GetInvitationsClient(ctx, 2002)
	if err != nil {
		t.Fatalf("failed to get client: %v", err)
	}

	// Create with 10 millisecond lifetime
	expiredInv, err := client.Create(ctx, "old@example.com", "admin", 0, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("failed to create: %v", err)
	}

	// Wait 20ms for it to expire
	time.Sleep(20 * time.Millisecond)

	// List should not return expired
	list, err := client.List(ctx)
	if err != nil {
		t.Fatalf("failed to list: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected expired invitation to be excluded from list, got %d", len(list))
	}

	// Get should return not found for expired
	if _, err := client.Get(ctx, expiredInv.Id); !errors.Is(err, invitations.ErrInvitationNotFound) {
		t.Fatalf("expected ErrInvitationNotFound for expired, got %v", err)
	}

	// Test max pending cap (use cap of 2)
	_, err = client.Create(ctx, "one@example.com", "admin", 0, time.Hour, 2)
	if err != nil {
		t.Fatalf("failed to create 1: %v", err)
	}
	_, err = client.Create(ctx, "two@example.com", "admin", 0, time.Hour, 2)
	if err != nil {
		t.Fatalf("failed to create 2: %v", err)
	}
	_, err = client.Create(ctx, "three@example.com", "admin", 0, time.Hour, 2)
	if !errors.Is(err, invitations.ErrTooManyInvitations) {
		t.Fatalf("expected ErrTooManyInvitations, got %v", err)
	}
}

func TestInvitations_MintAndVerifyToken(t *testing.T) {
	setupTest(t)
	ctx := context.Background()

	client, err := invitations.GetInvitationsClient(ctx, 3003)
	if err != nil {
		t.Fatalf("failed to get client: %v", err)
	}

	tm := auth.NewInvitationTokenManager("test-secret-key-32-bytes-long!!!")
	inv, err := client.Create(ctx, "member@example.com", "admin", 0, time.Hour)
	if err != nil {
		t.Fatalf("failed to create: %v", err)
	}

	token, err := client.MintToken(tm, inv)
	if err != nil {
		t.Fatalf("failed to mint token: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}

	claims, err := tm.VerifyInvitationToken(token)
	if err != nil {
		t.Fatalf("failed to verify token: %v", err)
	}
	if claims.ID != inv.Id {
		t.Errorf("expected claim ID %s, got %s", inv.Id, claims.ID)
	}
	if claims.TenantID != 3003 {
		t.Errorf("expected tenant ID 3003, got %d", claims.TenantID)
	}
}

func TestInvitations_URLHelpers(t *testing.T) {
	rawURL := "https://example.com/console.html?mode=stream&invite=tok123&email=user@test.com&tenant=1001"
	token, clean := invitations.StripInviteParams(rawURL)
	if token != "tok123" {
		t.Errorf("expected token tok123, got %s", token)
	}
	if clean != "https://example.com/console.html?mode=stream" {
		t.Errorf("expected clean URL without invite/tenant/email, got %s", clean)
	}

	decorated := invitations.DecorateInviteResult(clean, invitations.InviteResultAccepted, 1001)
	parsed, err := url.Parse(decorated)
	if err != nil {
		t.Fatalf("failed to parse decorated URL: %v", err)
	}
	if parsed.Query().Get(invitations.ParamInviteResult) != invitations.InviteResultAccepted {
		t.Errorf("expected invite_result accepted, got %s", parsed.Query().Get(invitations.ParamInviteResult))
	}
	if parsed.Query().Get(invitations.ParamInviteTarget) != "1001" {
		t.Errorf("expected invite_target 1001, got %s", parsed.Query().Get(invitations.ParamInviteTarget))
	}
	if parsed.Query().Get(invitations.ParamInviteTenant) != "1001" {
		t.Errorf("expected invite_tenant 1001, got %s", parsed.Query().Get(invitations.ParamInviteTenant))
	}
	if parsed.Query().Get("mode") != "stream" {
		t.Errorf("expected preserved mode param, got %s", parsed.Query().Get("mode"))
	}

	link := invitations.BuildInviteURL("https://example.com", "console.html", "my-token")
	if link != "https://example.com/console.html?invite=my-token" {
		t.Errorf("unexpected build URL: %s", link)
	}
}
