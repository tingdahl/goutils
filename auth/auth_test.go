package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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

func TestMicrosoftMultiTenantIssuerCheck(t *testing.T) {
	if !isMicrosoftMultiTenantIssuer("https://login.microsoftonline.com/common/v2.0") {
		t.Error("expected common to be recognized as Microsoft multi-tenant issuer")
	}
	if !isMicrosoftMultiTenantIssuer("https://login.microsoftonline.com/organizations/v2.0") {
		t.Error("expected organizations to be recognized as Microsoft multi-tenant issuer")
	}
	if !isMicrosoftMultiTenantIssuer("https://login.microsoftonline.com/consumers/v2.0") {
		t.Error("expected consumers to be recognized as Microsoft multi-tenant issuer")
	}
	if isMicrosoftMultiTenantIssuer("https://accounts.google.com") {
		t.Error("expected google to not be recognized as Microsoft multi-tenant issuer")
	}
}

func TestMsMultiTenantVerifier(t *testing.T) {
	ctx := context.Background()

	headerB64 := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`))
	sigB64 := base64.RawURLEncoding.EncodeToString([]byte("sig"))

	// Valid Microsoft issuer
	validPayload := buildTestJWTPayload("https://login.microsoftonline.com/tenant-123/v2.0", "sub-1", "user@ms.com", "MS User", true)
	validVerifier := createTestVerifier("https://login.microsoftonline.com/tenant-123/v2.0", "ms-client-id", validPayload)
	msVerifier := &msMultiTenantVerifier{underlying: validVerifier}

	validJWT := headerB64 + "." + base64.RawURLEncoding.EncodeToString(validPayload) + "." + sigB64
	token, err := msVerifier.Verify(ctx, validJWT)
	if err != nil || token == nil {
		t.Fatalf("expected verification to succeed for valid MS issuer, got: %v", err)
	}

	// Invalid issuer
	invalidPayload := buildTestJWTPayload("https://evil.com/v2.0", "sub-1", "user@evil.com", "Evil User", true)
	invalidVerifier := createTestVerifier("https://evil.com/v2.0", "ms-client-id", invalidPayload)
	msVerifierInvalid := &msMultiTenantVerifier{underlying: invalidVerifier}

	invalidJWT := headerB64 + "." + base64.RawURLEncoding.EncodeToString(invalidPayload) + "." + sigB64
	_, err = msVerifierInvalid.Verify(ctx, invalidJWT)
	if err == nil {
		t.Fatal("expected error for invalid Microsoft issuer")
	}
}

func TestInitOIDCRegistry_Success(t *testing.T) {
	server, _ := setupMockOIDCServer(t)

	configJSON := fmt.Sprintf(`{
		"auth_providers": [
			{
				"id": "mock-provider",
				"name": "Mock IDP",
				"issuer": %q,
				"client_id": "test-client-id",
				"client_secret": "test-secret"
			}
		]
	}`, server.URL)

	reg, err := InitOIDCRegistry(context.Background(), configJSON)
	if err != nil {
		t.Fatalf("InitOIDCRegistry failed: %v", err)
	}
	if len(reg.Providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(reg.Providers))
	}
	if reg.Providers[0].Config.ID != "mock-provider" {
		t.Errorf("expected provider ID mock-provider, got %s", reg.Providers[0].Config.ID)
	}
	if reg.Providers[0].ClientSecret != "test-secret" {
		t.Errorf("expected secret test-secret, got %s", reg.Providers[0].ClientSecret)
	}
}


func TestUserIdentity_StableSubject(t *testing.T) {
	// Microsoft normalized to tid:oid at parse time
	idMS := IdentityFromClaims(MicrosoftProviderID, "https://login.microsoftonline.com/tenant-abc/v2.0", "sub-pairwise", map[string]interface{}{
		ClaimTenantID: "tenant-abc",
		ClaimObjectID: "oid-xyz",
	})
	sub, err := idMS.StableSubject()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sub != "tenant-abc:oid-xyz" {
		t.Errorf("got %q, want tenant-abc:oid-xyz", sub)
	}
	if idMS.Claims[ClaimSubject] != "tenant-abc:oid-xyz" {
		t.Errorf("Claims[sub] = %q, want tenant-abc:oid-xyz", idMS.Claims[ClaimSubject])
	}

	// Google provider with standard sub
	idGoogle := IdentityFromClaims("google", GoogleIssuer, "google-sub-456", map[string]interface{}{
		ClaimSubject: "google-sub-456",
	})
	subGoogle, err := idGoogle.StableSubject()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if subGoogle != "google-sub-456" {
		t.Errorf("got %q, want google-sub-456", subGoogle)
	}
	if idGoogle.Claims[ClaimSubject] != "google-sub-456" {
		t.Errorf("Claims[sub] = %q, want google-sub-456", idGoogle.Claims[ClaimSubject])
	}

	// Identity with empty subject returns error
	idEmpty := UserIdentity{
		ProviderID: "google",
	}
	if _, err := idEmpty.StableSubject(); err == nil {
		t.Error("expected error for empty subject, got nil")
	}
}

func TestAuthMiddleware_MicrosoftEmailVerification(t *testing.T) {
	clientID := "ms-client-id"
	headerB64 := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`))
	sigB64 := base64.RawURLEncoding.EncodeToString([]byte("sig"))

	testCases := []struct {
		name         string
		claims       map[string]interface{}
		wantEmail    string
		wantVerified bool
		wantTenantID string
		wantObjectID string
	}{
		{
			name: "consumer tenant with preferred_username (no email)",
			claims: map[string]interface{}{
				"tid":                MicrosoftConsumerTenantID,
				"oid":                "oid-consumer-1",
				"preferred_username": "consumer@outlook.com",
			},
			wantEmail:    "consumer@outlook.com",
			wantVerified: true,
			wantTenantID: MicrosoftConsumerTenantID,
			wantObjectID: "oid-consumer-1",
		},
		{
			name: "consumer tenant with email claim",
			claims: map[string]interface{}{
				"tid":   MicrosoftConsumerTenantID,
				"oid":   "oid-consumer-2",
				"email": "user@live.com",
			},
			wantEmail:    "user@live.com",
			wantVerified: true,
			wantTenantID: MicrosoftConsumerTenantID,
			wantObjectID: "oid-consumer-2",
		},
		{
			name: "work tenant with preferred_username only (no email claim)",
			claims: map[string]interface{}{
				"tid":                "work-tenant-id",
				"oid":                "oid-work-1",
				"preferred_username": "alice@corp.com",
			},
			wantEmail:    "",
			wantVerified: false,
			wantTenantID: "work-tenant-id",
			wantObjectID: "oid-work-1",
		},
		{
			name: "work tenant with email and xms_edov true (bool)",
			claims: map[string]interface{}{
				"tid":      "work-tenant-id",
				"oid":      "oid-work-2",
				"email":    "alice@corp.com",
				"xms_edov": true,
			},
			wantEmail:    "alice@corp.com",
			wantVerified: true,
			wantTenantID: "work-tenant-id",
			wantObjectID: "oid-work-2",
		},
		{
			name: "work tenant with email and xms_edov string true",
			claims: map[string]interface{}{
				"tid":      "work-tenant-id",
				"oid":      "oid-work-3",
				"email":    "alice@corp.com",
				"xms_edov": "true",
			},
			wantEmail:    "alice@corp.com",
			wantVerified: true,
			wantTenantID: "work-tenant-id",
			wantObjectID: "oid-work-3",
		},
		{
			name: "work tenant with email and xms_edov string 1",
			claims: map[string]interface{}{
				"tid":      "work-tenant-id",
				"oid":      "oid-work-4",
				"email":    "alice@corp.com",
				"xms_edov": "1",
			},
			wantEmail:    "alice@corp.com",
			wantVerified: true,
			wantTenantID: "work-tenant-id",
			wantObjectID: "oid-work-4",
		},
		{
			name: "work tenant with email but without xms_edov",
			claims: map[string]interface{}{
				"tid":   "work-tenant-id",
				"oid":   "oid-work-5",
				"email": "attacker@victim.com",
			},
			wantEmail:    "attacker@victim.com",
			wantVerified: false,
			wantTenantID: "work-tenant-id",
			wantObjectID: "oid-work-5",
		},
		{
			name: "work tenant with email and xms_edov false",
			claims: map[string]interface{}{
				"tid":      "work-tenant-id",
				"oid":      "oid-work-6",
				"email":    "attacker@victim.com",
				"xms_edov": false,
			},
			wantEmail:    "attacker@victim.com",
			wantVerified: false,
			wantTenantID: "work-tenant-id",
			wantObjectID: "oid-work-6",
		},
		{
			name: "work tenant with explicit email_verified true is ignored without xms_edov",
			claims: map[string]interface{}{
				"tid":            "work-tenant-id",
				"oid":            "oid-work-7",
				"email":          "user@example.com",
				"email_verified": true,
			},
			wantEmail:    "user@example.com",
			wantVerified: false,
			wantTenantID: "work-tenant-id",
			wantObjectID: "oid-work-7",
		},
		{
			name: "consumer tenant with explicit email_verified false is honored",
			claims: map[string]interface{}{
				"tid":            MicrosoftConsumerTenantID,
				"oid":            "oid-consumer-3",
				"email":          "consumer@outlook.com",
				"email_verified": false,
			},
			wantEmail:    "consumer@outlook.com",
			wantVerified: false,
			wantTenantID: MicrosoftConsumerTenantID,
			wantObjectID: "oid-consumer-3",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			issuer := "https://login.microsoftonline.com/test-tenant/v2.0"
			m := map[string]interface{}{
				"iss": issuer,
				"sub": "sub-test",
				"aud": clientID,
				"exp": time.Now().Add(time.Hour).Unix(),
				"iat": time.Now().Unix(),
			}
			for k, v := range tc.claims {
				m[k] = v
			}
			jwtPayload, _ := json.Marshal(m)

			verifier := createTestVerifier(issuer, clientID, jwtPayload)
			registry := &OIDCRegistry{
				Providers: []RegisteredProvider{
					{
						Config:   AuthProviderConfig{ID: "microsoft", Issuer: issuer, ClientID: clientID},
						Verifier: verifier,
					},
				},
			}

			var capturedID UserIdentity
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedID, _ = GetUserIdentity(r.Context())
				w.WriteHeader(http.StatusOK)
			})

			testJWT := headerB64 + "." + base64.RawURLEncoding.EncodeToString(jwtPayload) + "." + sigB64
			req := httptest.NewRequest("GET", "/protected", nil)
			req.Header.Set("Authorization", "Bearer "+testJWT)
			rec := httptest.NewRecorder()

			AuthMiddleware(registry, nil, nil)(handler).ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d", rec.Code)
			}
			if capturedID.Email != tc.wantEmail {
				t.Errorf("Email = %q, want %q", capturedID.Email, tc.wantEmail)
			}
			if capturedID.EmailVerified != tc.wantVerified {
				t.Errorf("EmailVerified = %v, want %v", capturedID.EmailVerified, tc.wantVerified)
			}
			if tid, _ := capturedID.Claims[ClaimTenantID].(string); tid != tc.wantTenantID {
				t.Errorf("Claims[ClaimTenantID] = %q, want %q", tid, tc.wantTenantID)
			}
			if oid, _ := capturedID.Claims[ClaimObjectID].(string); oid != tc.wantObjectID {
				t.Errorf("Claims[ClaimObjectID] = %q, want %q", oid, tc.wantObjectID)
			}
			wantSub := tc.wantTenantID + ":" + tc.wantObjectID
			if capturedID.Subject != wantSub {
				t.Errorf("Subject = %q, want %q", capturedID.Subject, wantSub)
			}
		})
	}
}

func TestUserIdentity_SubjectAndSessionInfo(t *testing.T) {
	msParsed := IdentityFromClaims(ProviderMicrosoft, "https://login.microsoftonline.com/tenant-123/v2.0", "sub-ignore", map[string]interface{}{
		ClaimTenantID: "tenant-123",
		ClaimObjectID: "oid-456",
		ClaimEmail:    "user@work.com",
	})
	if msParsed.Subject != "tenant-123:oid-456" {
		t.Errorf("expected Subject tenant-123:oid-456, got %s", msParsed.Subject)
	}
	if tid, _ := msParsed.Claims[ClaimTenantID].(string); tid != "tenant-123" {
		t.Errorf("expected Claims[tid] tenant-123, got %s", tid)
	}
	if oid, _ := msParsed.Claims[ClaimObjectID].(string); oid != "oid-456" {
		t.Errorf("expected Claims[oid] oid-456, got %s", oid)
	}

	googleParsed := IdentityFromClaims(ProviderGoogle, GoogleIssuer, "google-sub-789", map[string]interface{}{
		ClaimEmail:         "user@gmail.com",
		ClaimEmailVerified: true,
	})
	if googleParsed.Subject != "google-sub-789" {
		t.Errorf("expected Subject google-sub-789, got %s", googleParsed.Subject)
	}

	sub, err := googleParsed.StableSubject()
	if err != nil || sub != "google-sub-789" {
		t.Errorf("unexpected StableSubject: %s, %v", sub, err)
	}

	ctx := context.Background()
	if _, ok := GetSessionInfo(ctx); ok {
		t.Error("expected false for missing SessionInfo")
	}
	ctx = WithSessionInfo(ctx, SessionInfo{Provider: "microsoft"})
	info, ok := GetSessionInfo(ctx)
	if !ok {
		t.Fatal("expected true for GetSessionInfo")
	}
	if info.Provider != "microsoft" {
		t.Errorf("unexpected SessionInfo: %+v", info)
	}
}
