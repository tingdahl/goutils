package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

type mockKeySet struct {
	payload []byte
}

func (m *mockKeySet) VerifySignature(ctx context.Context, jwt string) ([]byte, error) {
	if m.payload == nil {
		return nil, errors.New("signature verification failed")
	}
	return m.payload, nil
}

func TestOIDCRegistry_ConfigParsing(t *testing.T) {
	// Empty JSON
	_, err := InitOIDCRegistry(context.Background(), "")
	if err == nil {
		t.Error("expected error for empty config JSON")
	}

	// Invalid JSON
	_, err = InitOIDCRegistry(context.Background(), "{invalid json")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}

	// No auth providers
	_, err = InitOIDCRegistry(context.Background(), `{"auth_providers":[]}`)
	if err == nil {
		t.Error("expected error for empty auth_providers")
	}

	// Missing required fields
	_, err = InitOIDCRegistry(context.Background(), `{"auth_providers":[{"id":"google"}]}`)
	if err == nil {
		t.Error("expected error for missing issuer/client_id")
	}
}

func TestOIDCRegistry_Lookups(t *testing.T) {
	reg := &OIDCRegistry{
		Providers: []RegisteredProvider{
			{Config: AuthProviderConfig{ID: "google", ClientID: "client-google-123"}},
			{Config: AuthProviderConfig{ID: "microsoft", ClientID: "client-ms-456"}},
		},
	}

	if p := reg.GetProviderByID("google"); p == nil || p.Config.ClientID != "client-google-123" {
		t.Errorf("GetProviderByID(google) failed: %v", p)
	}
	if p := reg.GetProviderByID("nonexistent"); p != nil {
		t.Errorf("GetProviderByID(nonexistent) should be nil, got: %v", p)
	}

	if p := reg.GetProviderByClientID("client-ms-456"); p == nil || p.Config.ID != "microsoft" {
		t.Errorf("GetProviderByClientID(client-ms-456) failed: %v", p)
	}
	if p := reg.GetProviderByClientID("nonexistent"); p != nil {
		t.Errorf("GetProviderByClientID(nonexistent) should be nil, got: %v", p)
	}

	var nilReg *OIDCRegistry
	if nilReg.GetProviderByID("test") != nil {
		t.Error("nil registry should return nil")
	}
	if nilReg.GetProviderByClientID("test") != nil {
		t.Error("nil registry should return nil")
	}
}

func TestUserIdentity_Context(t *testing.T) {
	id := UserIdentity{
		ProviderID:    "google",
		Subject:       "sub-123",
		Email:         "user@example.com",
		EmailVerified: true,
		Name:          "Test User",
		Issuer:        "https://accounts.google.com",
	}

	ctx := WithUserIdentity(context.Background(), id)
	extracted, ok := GetUserIdentity(ctx)
	if !ok {
		t.Fatal("expected UserIdentity in context")
	}
	if extracted.Email != "user@example.com" || extracted.Subject != "sub-123" {
		t.Errorf("extracted identity mismatch: %+v", extracted)
	}

	_, ok = GetUserIdentity(context.Background())
	if ok {
		t.Error("expected ok=false for empty context")
	}
}

func createTestVerifier(issuer, clientID string, payload []byte) TokenVerifier {
	keySet := &mockKeySet{payload: payload}
	return oidc.NewVerifier(issuer, keySet, &oidc.Config{
		ClientID:          clientID,
		SkipClientIDCheck: true,
		SkipExpiryCheck:   true,
	})
}

func buildTestJWTPayload(issuer, subject, email, name string, emailVerified bool) []byte {
	m := map[string]interface{}{
		"iss":            issuer,
		"sub":            subject,
		"aud":            "test-client-id",
		"exp":            time.Now().Add(time.Hour).Unix(),
		"iat":            time.Now().Unix(),
		"email":          email,
		"email_verified": emailVerified,
		"name":           name,
	}
	b, _ := json.Marshal(m)
	return b
}

func TestAuthMiddleware(t *testing.T) {
	issuer := "https://accounts.example.com"
	clientID := "test-client-id"
	jwtPayload := buildTestJWTPayload(issuer, "sub-42", "alice@church.org", "Alice", true)

	verifier := createTestVerifier(issuer, clientID, jwtPayload)

	registry := &OIDCRegistry{
		Providers: []RegisteredProvider{
			{
				Config:   AuthProviderConfig{ID: "example-idp", Issuer: issuer, ClientID: clientID},
				Verifier: verifier,
			},
		},
	}

	// 1. Missing Authorization header
	mw := AuthMiddleware(registry, nil, nil)
	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	reqNoAuth := httptest.NewRequest("GET", "/protected", nil)
	recNoAuth := httptest.NewRecorder()
	mw(dummyHandler).ServeHTTP(recNoAuth, reqNoAuth)
	if recNoAuth.Code != http.StatusUnauthorized {
		t.Errorf("Missing header code = %d, want 401", recNoAuth.Code)
	}

	// 2. Malformed Authorization header (not Bearer)
	reqBadAuth := httptest.NewRequest("GET", "/protected", nil)
	reqBadAuth.Header.Set("Authorization", "Basic abc123")
	recBadAuth := httptest.NewRecorder()
	mw(dummyHandler).ServeHTTP(recBadAuth, reqBadAuth)
	if recBadAuth.Code != http.StatusUnauthorized {
		t.Errorf("Bad header code = %d, want 401", recBadAuth.Code)
	}

	// 3. Verification failure
	failingVerifier := createTestVerifier(issuer, clientID, nil) // nil payload causes signature error
	failRegistry := &OIDCRegistry{
		Providers: []RegisteredProvider{
			{
				Config:   AuthProviderConfig{ID: "failing-idp"},
				Verifier: failingVerifier,
			},
		},
	}
	reqToken := httptest.NewRequest("GET", "/protected", nil)
	reqToken.Header.Set("Authorization", "Bearer fake.jwt.token")
	recFail := httptest.NewRecorder()
	AuthMiddleware(failRegistry, nil, nil)(dummyHandler).ServeHTTP(recFail, reqToken)
	if recFail.Code != http.StatusUnauthorized {
		t.Errorf("Failing verification code = %d, want 401", recFail.Code)
	}

	// 4. Successful verification with IdentityResolver (simulating church/organization domain resolver)
	type appUserContextKey struct{}
	type ChurchUser struct {
		OrganizationID string
		Role           string
	}

	churchResolver := IdentityResolverFunc(func(ctx context.Context, id UserIdentity) (context.Context, error) {
		if id.Email == "blocked@church.org" {
			return nil, errors.New("user is blocked")
		}
		// Enrich context with church user
		user := &ChurchUser{
			OrganizationID: "church-central",
			Role:           "admin",
		}
		return context.WithValue(ctx, appUserContextKey{}, user), nil
	})

	var capturedIdentity UserIdentity
	var capturedChurchUser *ChurchUser
	successHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := GetUserIdentity(r.Context())
		if ok {
			capturedIdentity = id
		}
		if u, ok := r.Context().Value(appUserContextKey{}).(*ChurchUser); ok {
			capturedChurchUser = u
		}
		w.WriteHeader(http.StatusOK)
	})

	authMW := AuthMiddleware(registry, churchResolver, nil)
	headerB64 := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`))
	payloadB64 := base64.RawURLEncoding.EncodeToString(jwtPayload)
	sigB64 := base64.RawURLEncoding.EncodeToString([]byte("mock-sig"))
	testJWT := headerB64 + "." + payloadB64 + "." + sigB64

	reqSuccess := httptest.NewRequest("GET", "/protected", nil)
	reqSuccess.Header.Set("Authorization", "Bearer "+testJWT)
	recSuccess := httptest.NewRecorder()

	authMW(successHandler).ServeHTTP(recSuccess, reqSuccess)

	if recSuccess.Code != http.StatusOK {
		t.Fatalf("Success request code = %d, want 200", recSuccess.Code)
	}
	if capturedIdentity.Email != "alice@church.org" {
		t.Errorf("captured email = %q, want alice@church.org", capturedIdentity.Email)
	}
	if capturedIdentity.Subject != "sub-42" {
		t.Errorf("captured subject = %q, want sub-42", capturedIdentity.Subject)
	}
	if capturedChurchUser == nil || capturedChurchUser.OrganizationID != "church-central" {
		t.Errorf("captured church user mismatch: %+v", capturedChurchUser)
	}

	// 5. IdentityResolver rejects user
	blockedPayload := buildTestJWTPayload(issuer, "sub-99", "blocked@church.org", "Blocked", true)
	blockedVerifier := createTestVerifier(issuer, clientID, blockedPayload)
	blockedRegistry := &OIDCRegistry{
		Providers: []RegisteredProvider{
			{
				Config:   AuthProviderConfig{ID: "example-idp", Issuer: issuer, ClientID: clientID},
				Verifier: blockedVerifier,
			},
		},
	}
	recBlocked := httptest.NewRecorder()
	AuthMiddleware(blockedRegistry, churchResolver, nil)(dummyHandler).ServeHTTP(recBlocked, reqSuccess)
	if recBlocked.Code != http.StatusForbidden {
		t.Errorf("Blocked user code = %d, want 403", recBlocked.Code)
	}
}
