package auth_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tingdahl/goutils/auth"
	"github.com/tingdahl/goutils/jwt"
)

func TestSessionTokenManager_MintAndVerify(t *testing.T) {
	secret := "session-secret-key-12345"
	tm := auth.NewSessionTokenManager(secret)

	token, err := tm.MintUserSessionToken("usr_123", "alice@test.com", "Alice", "google", "")
	if err != nil {
		t.Fatalf("MintUserSessionToken failed: %v", err)
	}

	claims, err := tm.VerifyUserSessionToken(token)
	if err != nil {
		t.Fatalf("VerifyUserSessionToken failed: %v", err)
	}

	if claims.Sub != "usr_123" || claims.Email != "alice@test.com" || claims.Name != "Alice" || claims.Provider != "google" {
		t.Errorf("claims mismatch: %+v", claims)
	}
	if claims.Typ != auth.TokenTypUserSession {
		t.Errorf("expected typ %q, got %q", auth.TokenTypUserSession, claims.Typ)
	}
}

func TestSessionTokenManager_Expired(t *testing.T) {
	secret := "session-secret"
	tm := auth.NewSessionTokenManager(secret)

	token, err := tm.MintUserSessionTokenWithTTL("usr_1", "a@b.com", "A", "google", "", -1*time.Minute)
	if err != nil {
		t.Fatalf("failed to mint expired token: %v", err)
	}

	_, err = tm.VerifyUserSessionToken(token)
	if !errors.Is(err, jwt.ErrTokenExpired) {
		t.Errorf("expected ErrTokenExpired, got: %v", err)
	}
}

func TestSessionTokenManager_WrongTypRejected(t *testing.T) {
	secret := "session-secret"
	tm := auth.NewSessionTokenManager(secret)

	// Sign a token with a different typ (e.g. worker token) using same secret
	wrongClaims := struct {
		Typ string `json:"typ"`
		Sub string `json:"sub"`
		Exp int64  `json:"exp"`
	}{
		Typ: "worker",
		Sub: "worker-1",
		Exp: time.Now().Add(1 * time.Hour).Unix(),
	}
	foreignToken, err := jwt.Sign(wrongClaims, secret)
	if err != nil {
		t.Fatalf("failed to sign test token: %v", err)
	}

	_, err = tm.VerifyUserSessionToken(foreignToken)
	if !errors.Is(err, jwt.ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken for non-user_session typ, got: %v", err)
	}
}

func TestBuildSessionRedirect(t *testing.T) {
	redirect := auth.BuildSessionRedirect("https://example.com/app", "my.secret.token")
	expected := "https://example.com/app#token=my.secret.token"
	if redirect != expected {
		t.Errorf("expected %q, got %q", expected, redirect)
	}

	// Strips preexisting hash
	redirectWithHash := auth.BuildSessionRedirect("https://example.com/app#old", "token2")
	expected2 := "https://example.com/app#token=token2"
	if redirectWithHash != expected2 {
		t.Errorf("expected %q, got %q", expected2, redirectWithHash)
	}
}

func TestBuildAuthErrorRedirect(t *testing.T) {
	redirect := auth.BuildAuthErrorRedirect("https://example.com/app", "access_denied", "microsoft", "work", "alice@work.com")
	if redirect == "" {
		t.Fatal("expected non-empty redirect")
	}
	// Check query param error=access_denied
	if !testing.Short() {
		if !contains(redirect, "error=access_denied") {
			t.Errorf("expected error=access_denied in %s", redirect)
		}
		if !contains(redirect, "#") || !contains(redirect, "error_email=alice%40work.com") {
			t.Errorf("expected fragment error details in %s", redirect)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || (len(substr) > 0 && len(s) > 0 && (s[:len(substr)] == substr || contains(s[1:], substr))))
}

func TestSessionAuthMiddleware(t *testing.T) {
	secret := "mw-secret"
	tm := auth.NewSessionTokenManager(secret)
	tm.RenewalThreshold = 2 * time.Hour

	var resolvedUser string
	resolver := auth.SessionUserResolverFunc(func(ctx context.Context, claims *auth.UserSessionClaims) (context.Context, error) {
		resolvedUser = claims.Sub
		return ctx, nil
	})

	handler := auth.SessionAuthMiddleware(tm, resolver, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := auth.GetUserSessionClaims(r.Context())
		if !ok || claims == nil {
			t.Error("expected claims in context")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))

	// 1. Missing auth header -> 401
	req1 := httptest.NewRequest("GET", "/api/test", nil)
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for missing auth, got %d", rec1.Code)
	}

	// 2. Valid token -> 200
	validToken, _ := tm.MintUserSessionToken("user-999", "u@test.com", "U", "google", "")
	req2 := httptest.NewRequest("GET", "/api/test", nil)
	req2.Header.Set("Authorization", "Bearer "+validToken)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Errorf("expected 200 for valid token, got %d", rec2.Code)
	}
	if resolvedUser != "user-999" {
		t.Errorf("expected resolved user 'user-999', got %q", resolvedUser)
	}

	// 3. Rolling renewal: near expiration -> receives X-Renewed-Token
	nearExpToken, _ := tm.MintUserSessionTokenWithTTL("user-999", "u@test.com", "U", "google", "", 1*time.Hour)
	req3 := httptest.NewRequest("GET", "/api/test", nil)
	req3.Header.Set("Authorization", "Bearer "+nearExpToken)
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec3.Code)
	}
	renewed := rec3.Header().Get(auth.HeaderRenewedToken)
	if renewed == "" {
		t.Error("expected X-Renewed-Token header for token nearing expiration")
	} else {
		// Ensure renewed token is valid
		renewedClaims, err := tm.VerifyUserSessionToken(renewed)
		if err != nil {
			t.Fatalf("renewed token is invalid: %v", err)
		}
		if renewedClaims.Sub != "user-999" {
			t.Errorf("renewed token has wrong sub: %s", renewedClaims.Sub)
		}
	}
}
