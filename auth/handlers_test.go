package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

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
