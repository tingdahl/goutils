package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/tingdahl/goutils/jwt"
)

func TestInvitationTokenManager(t *testing.T) {
	secret := "test-invitation-secret-key-12345"
	tm := NewInvitationTokenManager(secret)

	// 1. Mint valid invitation token
	exp := time.Now().Add(48 * time.Hour)
	token, err := tm.MintInvitationToken("inv_12345", 99, exp)
	if err != nil {
		t.Fatalf("MintInvitationToken failed: %v", err)
	}

	// 2. Verify token
	claims, err := tm.VerifyInvitationToken(token)
	if err != nil {
		t.Fatalf("VerifyInvitationToken failed: %v", err)
	}
	if claims.Typ != TokenTypInvitation {
		t.Errorf("expected Typ %s, got %s", TokenTypInvitation, claims.Typ)
	}
	if claims.ID != "inv_12345" {
		t.Errorf("expected ID inv_12345, got %s", claims.ID)
	}
	if claims.TenantID != 99 {
		t.Errorf("expected TenantID 99, got %d", claims.TenantID)
	}

	// 3. Expired token
	expiredTok, err := tm.MintInvitationToken("inv_expired", 99, time.Now().Add(-1*time.Minute))
	if err != nil {
		t.Fatalf("MintInvitationToken expired failed: %v", err)
	}
	if _, err := tm.VerifyInvitationToken(expiredTok); !errors.Is(err, jwt.ErrTokenExpired) {
		t.Errorf("expected ErrTokenExpired, got %v", err)
	}

	// 4. Wrong secret
	otherTM := NewInvitationTokenManager("different-secret")
	if _, err := otherTM.VerifyInvitationToken(token); err == nil {
		t.Error("expected error verifying with wrong secret, got nil")
	}

	// 5. Wrong token type (e.g. session token)
	sessionTM := NewSessionTokenManager(secret)
	sessionTok, _ := sessionTM.MintUserSessionToken("usr_123", "google")
	if _, err := tm.VerifyInvitationToken(sessionTok); err == nil {
		t.Error("expected error verifying session token as invitation token, got nil")
	}
}
