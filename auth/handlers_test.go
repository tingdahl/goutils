package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gorilla/mux"
)

func TestPublicProviders(t *testing.T) {
	registry := &OIDCRegistry{
		Providers: []RegisteredProvider{
			{
				Config: AuthProviderConfig{
					ID:           "google",
					Name:         "Google",
					Icon:         "google",
					RedirectPath: "/auth/callback/google",
				},
			},
			{
				Config: AuthProviderConfig{
					ID:   "microsoft",
					Name: "Microsoft Entra ID",
				},
			},
		},
	}

	providers := registry.PublicProviders()
	if len(providers) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(providers))
	}
	if providers[0].ID != "google" || providers[0].RedirectPath != "/auth/callback/google" {
		t.Errorf("unexpected provider 0: %+v", providers[0])
	}
	if providers[1].ID != "microsoft" || providers[1].RedirectPath != "/auth/callback/microsoft" {
		t.Errorf("unexpected default redirect path for provider 1: %+v", providers[1])
	}
}

func TestLoginHandler_Unconfigured(t *testing.T) {
	registry := &OIDCRegistry{}
	handler := registry.LoginHandler(nil)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/google/login", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503 for unconfigured OIDC, got %d", w.Code)
	}
}

func TestLoginHandler_NotFound(t *testing.T) {
	registry := &OIDCRegistry{
		Providers: []RegisteredProvider{
			{
				Config: AuthProviderConfig{
					ID:       "google",
					Name:     "Google",
					ClientID: "mock-client-id",
				},
			},
		},
	}
	router := mux.NewRouter()
	registry.RegisterRoutes(router, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/unknown/login", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected status 404 for unknown provider, got %d", w.Code)
	}
}

func TestCallbackHandler_MissingState(t *testing.T) {
	registry := &OIDCRegistry{
		Providers: []RegisteredProvider{
			{
				Config: AuthProviderConfig{
					ID:           "google",
					Name:         "Google",
					ClientID:     "mock-client-id",
					RedirectPath: "/auth/callback/google",
				},
			},
		},
	}
	router := mux.NewRouter()
	registry.RegisterRoutes(router, nil)

	req := httptest.NewRequest(http.MethodGet, "/auth/callback/google", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for missing state, got %d", w.Code)
	}
}

func TestTokenHandler_MissingCode(t *testing.T) {
	registry := &OIDCRegistry{}
	handler := registry.TokenHandler()

	form := url.Values{}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for missing code, got %d", w.Code)
	}
}

func setupMockOIDCServer(t *testing.T) (*httptest.Server, *oidc.Provider) {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"issuer":                 server.URL,
				"authorization_endpoint": server.URL + "/authorize",
				"token_endpoint":         server.URL + "/token",
				"jwks_uri":               server.URL + "/jwks",
				"response_types_supported": []string{"code", "id_token"},
				"subject_types_supported":  []string{"public"},
				"id_token_signing_alg_values_supported": []string{"RS256"},
			})
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"access_token": "mock-access-token",
				"token_type":   "Bearer",
				"expires_in":   3600,
				"id_token":     "mock.id.token",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	provider, err := oidc.NewProvider(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("failed to create oidc provider from mock server: %v", err)
	}
	return server, provider
}

func TestLoginHandler_SuccessRedirect(t *testing.T) {
	server, provider := setupMockOIDCServer(t)

	registry := &OIDCRegistry{
		Providers: []RegisteredProvider{
			{
				Config: AuthProviderConfig{
					ID:           "test-idp",
					Name:         "Test IDP",
					ClientID:     "test-client-123",
					ClientSecret: "test-secret-456",
					RedirectPath: "/auth/callback/test-idp",
				},
				Provider: provider,
			},
		},
	}

	router := mux.NewRouter()
	registry.RegisterRoutes(router, &AuthRoutesOptions{
		DefaultReturnTo: "/dashboard",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/auth/test-idp/login?return_to=/custom-return", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "app.example.com")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("expected status 302, got %d", w.Code)
	}

	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, server.URL+"/authorize?") {
		t.Errorf("expected redirect to authorize endpoint, got %s", loc)
	}
	if !strings.Contains(loc, "client_id=test-client-123") {
		t.Errorf("missing client_id in auth URL: %s", loc)
	}
	if !strings.Contains(loc, "redirect_uri=https%3A%2F%2Fapp.example.com%2Fauth%2Fcallback%2Ftest-idp") {
		t.Errorf("missing or incorrect redirect_uri: %s", loc)
	}

	// Verify cookies
	cookies := w.Result().Cookies()
	var stateCookie, returnCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "oidc_state_test-idp" {
			stateCookie = c
		}
		if c.Name == "oidc_return_to" {
			returnCookie = c
		}
	}
	if stateCookie == nil || stateCookie.Value == "" {
		t.Errorf("missing state cookie")
	}
	if returnCookie == nil || returnCookie.Value != "/custom-return" {
		t.Errorf("missing or incorrect return_to cookie: %v", returnCookie)
	}
}

func TestCallbackHandler_ErrorFromIdP(t *testing.T) {
	server, provider := setupMockOIDCServer(t)
	_ = server

	registry := &OIDCRegistry{
		Providers: []RegisteredProvider{
			{
				Config: AuthProviderConfig{
					ID:       "test-idp",
					ClientID: "test-client",
				},
				Provider: provider,
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/test-idp/callback?error=access_denied&error_description=user+canceled", nil)
	w := httptest.NewRecorder()
	registry.CallbackHandler(nil)(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for IdP error, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "access_denied") {
		t.Errorf("expected body to contain error description, got %s", w.Body.String())
	}
}

func TestCallbackHandler_StateMismatch(t *testing.T) {
	server, provider := setupMockOIDCServer(t)
	_ = server

	registry := &OIDCRegistry{
		Providers: []RegisteredProvider{
			{
				Config: AuthProviderConfig{
					ID:       "test-idp",
					ClientID: "test-client",
				},
				Provider: provider,
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/test-idp/callback?state=state-from-query&code=code123", nil)
	req.AddCookie(&http.Cookie{
		Name:  "oidc_state_test-idp",
		Value: "different-state-in-cookie",
	})
	w := httptest.NewRecorder()
	registry.CallbackHandler(nil)(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for state mismatch, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "State mismatch") {
		t.Errorf("expected State mismatch error, got: %s", w.Body.String())
	}
}

func TestTokenHandler_ProxySuccess(t *testing.T) {
	server, provider := setupMockOIDCServer(t)
	_ = server

	registry := &OIDCRegistry{
		Providers: []RegisteredProvider{
			{
				Config: AuthProviderConfig{
					ID:           "test-idp",
					ClientID:     "test-client",
					ClientSecret: "secret",
				},
				Provider: provider,
			},
		},
	}

	form := url.Values{}
	form.Set("code", "valid-code")
	form.Set("provider_id", "test-idp")
	form.Set("client_id", "test-client")
	form.Set("redirect_uri", "http://localhost/callback")

	req := httptest.NewRequest(http.MethodPost, "/api/auth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	registry.TokenHandler()(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "mock-access-token") {
		t.Errorf("expected token response, got: %s", w.Body.String())
	}
}

