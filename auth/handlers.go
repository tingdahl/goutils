package auth

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/tingdahl/goutils/config"
	"golang.org/x/oauth2"
)

// PublicProviderInfo exposes safe metadata for OIDC login buttons without sensitive secrets.
type PublicProviderInfo struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Icon         string `json:"icon,omitempty"`
	RedirectPath string `json:"redirect_path,omitempty"`
}

// PublicProviders extracts safe public metadata from an OIDC registry.
func (r *OIDCRegistry) PublicProviders() []PublicProviderInfo {
	if r == nil || len(r.Providers) == 0 {
		return []PublicProviderInfo{}
	}

	result := make([]PublicProviderInfo, 0, len(r.Providers))
	for _, p := range r.Providers {
		redirectPath := p.Config.RedirectPath
		if redirectPath == "" {
			redirectPath = "/auth/callback/" + p.Config.ID
		}
		result = append(result, PublicProviderInfo{
			ID:           p.Config.ID,
			Name:         p.Config.Name,
			Icon:         p.Config.Icon,
			RedirectPath: redirectPath,
		})
	}
	return result
}

// AuthRoutesOptions configures the standard OIDC routes mounted by OIDCRegistry.
type AuthRoutesOptions struct {
	// LoginPathPrefix is the path prefix for initiating login.
	// Defaults to "/api/auth".
	LoginPathPrefix string

	// DefaultReturnTo is where to redirect after successful callback if no return_to parameter is given.
	// Defaults to "/".
	DefaultReturnTo string

	// OnSuccess is an optional hook called on successful authentication callback.
	// If nil, the handler redirects to returnTo with "?token=<rawIDToken>".
	OnSuccess func(w http.ResponseWriter, r *http.Request, provider *RegisteredProvider, rawIDToken string, returnTo string)
}

func (r *OIDCRegistry) getProvider(req *http.Request) *RegisteredProvider {
	if r == nil {
		return nil
	}

	vars := mux.Vars(req)
	id := vars["id"]
	if id == "" {
		id = req.URL.Query().Get("provider_id")
	}
	if id == "" {
		id = req.URL.Query().Get("provider")
	}

	if id != "" {
		return r.GetProviderByID(id)
	}

	// Try matching by redirect_path
	reqPath := req.URL.Path
	for i := range r.Providers {
		p := &r.Providers[i]
		if p.Config.RedirectPath == reqPath {
			return p
		}
	}

	// If no ID was provided and only 1 provider exists, fallback to it
	if len(r.Providers) == 1 {
		return &r.Providers[0]
	}

	return nil
}

func (r *OIDCRegistry) getRedirectURI(req *http.Request, p *RegisteredProvider) string {
	redirectPath := p.Config.RedirectPath
	if redirectPath == "" {
		redirectPath = "/auth/callback/" + p.Config.ID
	}

	scheme := "http"
	if req.TLS != nil || req.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}

	host := req.Host
	if fwdHost := req.Header.Get("X-Forwarded-Host"); fwdHost != "" {
		host = fwdHost
	}

	return fmt.Sprintf("%s://%s%s", scheme, host, redirectPath)
}

func (r *OIDCRegistry) getClientSecret(p *RegisteredProvider) string {
	secret := p.ClientSecret
	if secret == "" && p.Config.SecretName != "" {
		secret = config.Config().GetConfigString(p.Config.SecretName)
	}
	if secret == "" && p.Config.ClientSecret != "" {
		secret = p.Config.ClientSecret
	}
	return secret
}

// LoginHandler returns an HTTP handler that initiates OIDC login by redirecting to the provider.
func (r *OIDCRegistry) LoginHandler(opts *AuthRoutesOptions) http.HandlerFunc {
	defaultReturnTo := "/"
	if opts != nil && opts.DefaultReturnTo != "" {
		defaultReturnTo = opts.DefaultReturnTo
	}

	return func(w http.ResponseWriter, req *http.Request) {
		if r == nil || len(r.Providers) == 0 {
			http.Error(w, "OIDC authentication is not configured on this server", http.StatusServiceUnavailable)
			return
		}

		p := r.getProvider(req)
		if p == nil {
			http.Error(w, "Authentication provider not found", http.StatusNotFound)
			return
		}

		if p.Provider == nil {
			http.Error(w, "Provider endpoint not initialized", http.StatusInternalServerError)
			return
		}

		isSecure := req.TLS != nil || req.Header.Get("X-Forwarded-Proto") == "https"

		returnTo := req.URL.Query().Get("return_to")
		if returnTo == "" {
			returnTo = defaultReturnTo
		}

		// Generate random state token with encoded returnTo payload
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		randomHex := hex.EncodeToString(b)
		exp := time.Now().Add(15 * time.Minute).Unix()
		returnToB64 := base64.RawURLEncoding.EncodeToString([]byte(returnTo))
		state := fmt.Sprintf("%s:%d:%s:%s", p.Config.ID, exp, randomHex, returnToB64)

		cookieName := "oidc_state_" + p.Config.ID
		http.SetCookie(w, &http.Cookie{
			Name:     cookieName,
			Value:    state,
			Path:     "/",
			HttpOnly: true,
			Secure:   isSecure,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   900,
		})

		http.SetCookie(w, &http.Cookie{
			Name:     "oidc_return_to",
			Value:    returnTo,
			Path:     "/",
			HttpOnly: true,
			Secure:   isSecure,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   900,
		})

		redirectURI := r.getRedirectURI(req, p)
		clientSecret := r.getClientSecret(p)

		scopes := p.Config.Scopes
		if len(scopes) == 0 {
			scopes = []string{"openid", "email", "profile"}
		}
		hasOpenID := false
		for _, sc := range scopes {
			if sc == "openid" {
				hasOpenID = true
				break
			}
		}
		if !hasOpenID {
			scopes = append([]string{"openid"}, scopes...)
		}

		oauth2Config := &oauth2.Config{
			ClientID:     p.Config.ClientID,
			ClientSecret: clientSecret,
			Endpoint:     p.Provider.Endpoint(),
			RedirectURL:  redirectURI,
			Scopes:       scopes,
		}

		authURL := oauth2Config.AuthCodeURL(state)
		http.Redirect(w, req, authURL, http.StatusFound)
	}
}

// CallbackHandler returns an HTTP handler that processes OIDC callbacks and verifies the ID token.
func (r *OIDCRegistry) CallbackHandler(opts *AuthRoutesOptions) http.HandlerFunc {
	defaultReturnTo := "/"
	if opts != nil && opts.DefaultReturnTo != "" {
		defaultReturnTo = opts.DefaultReturnTo
	}

	return func(w http.ResponseWriter, req *http.Request) {
		if r == nil || len(r.Providers) == 0 {
			http.Error(w, "OIDC authentication is not configured on this server", http.StatusServiceUnavailable)
			return
		}

		p := r.getProvider(req)
		if p == nil {
			http.Error(w, "Authentication provider not found for callback", http.StatusNotFound)
			return
		}

		if errParam := req.URL.Query().Get("error"); errParam != "" {
			errDesc := req.URL.Query().Get("error_description")
			slog.Warn("Identity provider returned error", "provider", p.Config.ID, "error", errParam, "desc", errDesc)
			http.Error(w, fmt.Sprintf("Authentication failed: %s (%s)", errParam, errDesc), http.StatusBadRequest)
			return
		}

		state := req.URL.Query().Get("state")
		if state == "" {
			http.Error(w, "Missing state parameter", http.StatusBadRequest)
			return
		}

		cookieName := "oidc_state_" + p.Config.ID
		stateCookie, _ := req.Cookie(cookieName)
		if stateCookie != nil && stateCookie.Value != "" {
			if stateCookie.Value != state {
				http.Error(w, "State mismatch error", http.StatusBadRequest)
				return
			}
		} else {
			parts := strings.Split(state, ":")
			if len(parts) >= 2 {
				exp, err := strconv.ParseInt(parts[1], 10, 64)
				if err != nil || time.Now().Unix() > exp {
					http.Error(w, "State has expired", http.StatusBadRequest)
					return
				}
			}
		}

		http.SetCookie(w, &http.Cookie{
			Name:     cookieName,
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
		})

		code := req.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "Missing authorization code in callback", http.StatusBadRequest)
			return
		}

		redirectURI := r.getRedirectURI(req, p)
		clientSecret := r.getClientSecret(p)

		oauth2Config := &oauth2.Config{
			ClientID:     p.Config.ClientID,
			ClientSecret: clientSecret,
			Endpoint:     p.Provider.Endpoint(),
			RedirectURL:  redirectURI,
			Scopes:       p.Config.Scopes,
		}

		oauth2Token, err := oauth2Config.Exchange(req.Context(), code)
		if err != nil {
			slog.Error("Failed to exchange authorization code for token", "provider", p.Config.ID, "error", err)
			http.Error(w, "Failed to exchange authorization code with identity provider: "+err.Error(), http.StatusBadGateway)
			return
		}

		rawIDToken, ok := oauth2Token.Extra("id_token").(string)
		if !ok || rawIDToken == "" {
			slog.Error("Identity provider response missing id_token", "provider", p.Config.ID)
			http.Error(w, "Identity provider did not return an id_token", http.StatusBadGateway)
			return
		}

		if p.Verifier != nil {
			if _, err := p.Verifier.Verify(req.Context(), rawIDToken); err != nil {
				slog.Error("Failed to verify ID token", "provider", p.Config.ID, "error", err)
				http.Error(w, "Failed to verify ID token: "+err.Error(), http.StatusUnauthorized)
				return
			}
		}

		isSecure := req.TLS != nil || req.Header.Get("X-Forwarded-Proto") == "https"
		returnTo := defaultReturnTo
		if retCookie, err := req.Cookie("oidc_return_to"); err == nil && retCookie.Value != "" {
			returnTo = retCookie.Value
			http.SetCookie(w, &http.Cookie{
				Name:     "oidc_return_to",
				Value:    "",
				Path:     "/",
				MaxAge:   -1,
				HttpOnly: true,
				Secure:   isSecure,
			})
		} else {
			// Fallback: extract return_to from state parameter
			parts := strings.Split(state, ":")
			if len(parts) >= 4 {
				if decoded, err := base64.RawURLEncoding.DecodeString(parts[3]); err == nil && len(decoded) > 0 {
					returnTo = string(decoded)
				}
			}
		}

		if opts != nil && opts.OnSuccess != nil {
			opts.OnSuccess(w, req, p, rawIDToken, returnTo)
			return
		}

		sep := "?"
		if strings.Contains(returnTo, "?") {
			sep = "&"
		}
		targetURL := fmt.Sprintf("%s%stoken=%s", returnTo, sep, url.QueryEscape(rawIDToken))
		http.Redirect(w, req, targetURL, http.StatusFound)
	}
}

// TokenHandler handles POST token exchange proxy requests (for client-side PKCE flows).
func (r *OIDCRegistry) TokenHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}

		req.Body = http.MaxBytesReader(w, req.Body, 64<<10)
		if err := req.ParseForm(); err != nil {
			http.Error(w, "Failed to parse form", http.StatusBadRequest)
			return
		}

		code := req.FormValue("code")
		codeVerifier := req.FormValue("code_verifier")
		redirectURI := req.FormValue("redirect_uri")
		grantType := req.FormValue("grant_type")
		clientID := req.FormValue("client_id")
		providerID := req.FormValue("provider_id")

		if code == "" {
			http.Error(w, "Missing code", http.StatusBadRequest)
			return
		}
		if grantType == "" {
			grantType = "authorization_code"
		}

		p := r.getProvider(req)
		if p == nil && providerID != "" && r != nil {
			p = r.GetProviderByID(providerID)
		}
		if p == nil && clientID != "" && r != nil {
			p = r.GetProviderByClientID(clientID)
		}
		if p == nil && r != nil && len(r.Providers) == 1 {
			p = &r.Providers[0]
		}

		if p == nil || p.Provider == nil {
			http.Error(w, "Unable to resolve identity provider", http.StatusBadRequest)
			return
		}

		tokenURL := p.Provider.Endpoint().TokenURL
		clientSecret := r.getClientSecret(p)
		if clientID == "" {
			clientID = p.Config.ClientID
		}

		form := url.Values{}
		form.Set("grant_type", grantType)
		form.Set("code", code)
		form.Set("client_id", clientID)
		if clientSecret != "" {
			form.Set("client_secret", clientSecret)
		}
		if codeVerifier != "" {
			form.Set("code_verifier", codeVerifier)
		}
		if redirectURI != "" {
			form.Set("redirect_uri", redirectURI)
		}

		upstreamReq, err := http.NewRequestWithContext(req.Context(), http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
		if err != nil {
			http.Error(w, "Failed to create upstream token request", http.StatusInternalServerError)
			return
		}
		upstreamReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		upstreamReq.Header.Set("Accept", "application/json")

		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(upstreamReq)
		if err != nil {
			slog.Error("Failed to proxy token request to identity provider", "error", err)
			http.Error(w, "Failed to connect to identity provider", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			http.Error(w, "Failed to read response from identity provider", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		w.Write(body)
	}
}

// RegisterRoutes registers standard OIDC authentication endpoints onto a gorilla/mux router.
func (r *OIDCRegistry) RegisterRoutes(router *mux.Router, opts *AuthRoutesOptions) {
	prefix := "/api/auth"
	if opts != nil && opts.LoginPathPrefix != "" {
		prefix = opts.LoginPathPrefix
	}
	prefix = strings.TrimSuffix(prefix, "/")

	loginHandler := r.LoginHandler(opts)
	callbackHandler := r.CallbackHandler(opts)
	tokenHandler := r.TokenHandler()

	// Login routes
	router.HandleFunc(prefix+"/{id}/login", loginHandler).Methods(http.MethodGet)
	if prefix != "/auth" {
		router.HandleFunc("/auth/{id}/login", loginHandler).Methods(http.MethodGet)
	}

	// Callback routes
	router.HandleFunc(prefix+"/{id}/callback", callbackHandler).Methods(http.MethodGet)
	router.HandleFunc("/auth/callback/{id}", callbackHandler).Methods(http.MethodGet)

	// Token proxy route
	router.HandleFunc(prefix+"/token", tokenHandler).Methods(http.MethodPost)

	// Register specific redirect paths if configured
	if r != nil {
		for _, p := range r.Providers {
			if p.Config.RedirectPath != "" {
				router.HandleFunc(p.Config.RedirectPath, callbackHandler).Methods(http.MethodGet)
			}
		}
	}
}
