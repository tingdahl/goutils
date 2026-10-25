package auth_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tingdahl/goutils/auth"
	"github.com/tingdahl/goutils/jwt"
)

func TestSessionTokenManager_MintAndVerify(t *testing.T) {
	secret := "session-secret-key-12345"
	tm := auth.NewSessionTokenManager(secret)

	token, err := tm.MintUserSessionToken("usr_123", "microsoft", auth.MicrosoftConsumerTenantID)
	if err != nil {
		t.Fatalf("MintUserSessionToken failed: %v", err)
	}

	claims, err := tm.VerifyUserSessionToken(token)
	if err != nil {
		t.Fatalf("VerifyUserSessionToken failed: %v", err)
	}

	if claims.Sub != "usr_123" || claims.Provider != "microsoft" || claims.TenantID != auth.MicrosoftConsumerTenantID {
		t.Errorf("claims mismatch: %+v", claims)
	}
	if claims.Typ != auth.TokenTypUserSession {
		t.Errorf("expected typ %q, got %q", auth.TokenTypUserSession, claims.Typ)
	}
}

func TestSessionTokenManager_Expired(t *testing.T) {
	secret := "session-secret"
	tm := auth.NewSessionTokenManager(secret)

	token, err := tm.MintUserSessionTokenWithTTL("usr_1", "google", "", -1*time.Minute)
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
}

func TestBuildAuthErrorRedirect(t *testing.T) {
	redirect := auth.BuildAuthErrorRedirect("https://example.com/app", "access_denied", "microsoft", auth.MicrosoftConsumerTenantID, "alice@work.com")
	if !strings.HasPrefix(redirect, "https://example.com/app#") {
		t.Fatalf("expected fragment redirect, got %s", redirect)
	}
	if !strings.Contains(redirect, "error=access_denied") {
		t.Errorf("expected error=access_denied in %s", redirect)
	}
	if !strings.Contains(redirect, "error_provider=microsoft") {
		t.Errorf("expected error_provider=microsoft in %s", redirect)
	}
	if !strings.Contains(redirect, "error_tenant_id="+auth.MicrosoftConsumerTenantID) {
		t.Errorf("expected error_tenant_id in %s", redirect)
	}
	if !strings.Contains(redirect, "error_email=alice%40work.com") {
		t.Errorf("expected error_email in %s", redirect)
	}
}

type testSessionResolver struct {
	resolvedSub string
}

func (r *testSessionResolver) ResolveSessionUser(ctx context.Context, claims *auth.UserSessionClaims) (context.Context, error) {
	r.resolvedSub = claims.Sub
	return ctx, nil
}

func TestSessionAuthMiddleware(t *testing.T) {
	secret := "mw-secret"
	tm := auth.NewSessionTokenManager(secret)
	tm.RenewalThreshold = 2 * time.Hour

	resolver := &testSessionResolver{}

	handler := auth.SessionAuthMiddleware(tm, resolver, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info, ok := auth.GetSessionInfo(r.Context())
		if !ok || info.Provider != "google" {
			t.Errorf("expected session info with provider google, got %+v", info)
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
	validToken, _ := tm.MintUserSessionToken("user-999", "google", "")
	req2 := httptest.NewRequest("GET", "/api/test", nil)
	req2.Header.Set("Authorization", "Bearer "+validToken)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Errorf("expected 200 for valid token, got %d", rec2.Code)
	}
	if resolver.resolvedSub != "user-999" {
		t.Errorf("expected resolved user user-999, got %q", resolver.resolvedSub)
	}

	// 3. Rolling renewal: near expiration -> receives X-Renewed-Token
	nearExpToken, _ := tm.MintUserSessionTokenWithTTL("user-999", "google", "", 1*time.Hour)
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
		renewedClaims, err := tm.VerifyUserSessionToken(renewed)
		if err != nil {
			t.Fatalf("renewed token is invalid: %v", err)
		}
		if renewedClaims.Sub != "user-999" {
			t.Errorf("renewed token has wrong sub: %s", renewedClaims.Sub)
		}
		if renewedClaims.Provider != "google" {
			t.Errorf("renewed token has wrong provider: %s", renewedClaims.Provider)
		}
	}
}
