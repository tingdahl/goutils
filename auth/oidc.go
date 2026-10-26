package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/tingdahl/goutils/config"
)

// AuthProviderConfig holds static configuration for an OIDC identity provider.
type AuthProviderConfig struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Icon         string   `json:"icon,omitempty"`
	Issuer       string   `json:"issuer"`
	ClientID     string   `json:"client_id,omitempty"`
	ClientIDName string   `json:"client_id_name,omitempty"`
	SecretName   string   `json:"secret_name,omitempty"`
	ClientSecret string   `json:"client_secret,omitempty"`
	RedirectPath string   `json:"redirect_path,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`

	// Static endpoints (optional; if set, OIDC discovery network requests are bypassed)
	AuthURL  string `json:"auth_url,omitempty"`
	TokenURL string `json:"token_url,omitempty"`
	JWKSURL  string `json:"jwks_url,omitempty"`
}

// OIDCConfigPayload is the top-level container for provider configuration in JSON.
type OIDCConfigPayload struct {
	AuthProviders []AuthProviderConfig `json:"auth_providers"`
}

// TokenVerifier defines the interface needed for verifying ID tokens.
// *oidc.IDTokenVerifier satisfies this interface.
type TokenVerifier interface {
	Verify(ctx context.Context, rawIDToken string) (*oidc.IDToken, error)
}

// RegisteredProvider wraps an initialized OIDC provider and verifier.
type RegisteredProvider struct {
	Config       AuthProviderConfig
	Provider     *oidc.Provider
	Verifier     TokenVerifier
	ClientSecret string
	initOnce     *sync.Once
	initErr      error
}

// EnsureInitialized initializes provider endpoints and token verifier if not already done.
// If static endpoints (AuthURL, TokenURL, JWKSURL) were configured, initialization happens
// instantly with zero network calls. Otherwise, discovery runs lazily on first access.
func (p *RegisteredProvider) EnsureInitialized(ctx context.Context) error {
	if p == nil {
		return fmt.Errorf("provider is nil")
	}
	if p.Provider != nil {
		return nil
	}
	if p.Config.Issuer == "" {
		return fmt.Errorf("provider %q has no issuer configured", p.Config.ID)
	}
	if p.initOnce == nil {
		p.initOnce = new(sync.Once)
	}
	p.initOnce.Do(func() {
		p.initErr = p.initProvider(ctx)
	})
	return p.initErr
}

func (p *RegisteredProvider) initProvider(ctx context.Context) error {
	if p.Provider != nil && p.Verifier != nil {
		return nil
	}

	clientID := p.Config.ClientID
	isMSMultiTenant := isMicrosoftMultiTenantIssuer(p.Config.Issuer)

	if p.Config.AuthURL != "" && p.Config.TokenURL != "" && p.Config.JWKSURL != "" {
		cfg := &oidc.ProviderConfig{
			IssuerURL: p.Config.Issuer,
			AuthURL:   p.Config.AuthURL,
			TokenURL:  p.Config.TokenURL,
			JWKSURL:   p.Config.JWKSURL,
		}
		p.Provider = cfg.NewProvider(ctx)
	} else {
		providerCtx := ctx
		if isMSMultiTenant {
			providerCtx = oidc.InsecureIssuerURLContext(ctx, "https://login.microsoftonline.com/{tenantid}/v2.0")
		}
		provider, err := oidc.NewProvider(providerCtx, p.Config.Issuer)
		if err != nil {
			return fmt.Errorf("error initializing OIDC provider %q (%s): %w", p.Config.ID, p.Config.Issuer, err)
		}
		p.Provider = provider
	}

	verifierConfig := &oidc.Config{ClientID: clientID}
	if isMSMultiTenant {
		verifierConfig.SkipIssuerCheck = true
	}
	var verifier TokenVerifier = p.Provider.Verifier(verifierConfig)
	if isMSMultiTenant {
		verifier = &msMultiTenantVerifier{underlying: verifier}
	}
	p.Verifier = verifier
	return nil
}

// OIDCRegistry manages a collection of initialized OIDC providers.
type OIDCRegistry struct {
	Providers []RegisteredProvider
}

// GetProviderByID returns a registered provider by its unique ID.
func (r *OIDCRegistry) GetProviderByID(id string) *RegisteredProvider {
	if r == nil {
		return nil
	}
	for i := range r.Providers {
		if r.Providers[i].Config.ID == id {
			return &r.Providers[i]
		}
	}
	return nil
}

// GetProviderByClientID returns a registered provider matching a ClientID.
func (r *OIDCRegistry) GetProviderByClientID(clientID string) *RegisteredProvider {
	if r == nil {
		return nil
	}
	for i := range r.Providers {
		if r.Providers[i].Config.ClientID == clientID {
			return &r.Providers[i]
		}
	}
	return nil
}

func isMicrosoftMultiTenantIssuer(issuer string) bool {
	return strings.Contains(issuer, "login.microsoftonline.com/common") ||
		strings.Contains(issuer, "login.microsoftonline.com/organizations") ||
		strings.Contains(issuer, "login.microsoftonline.com/consumers")
}

type msMultiTenantVerifier struct {
	underlying TokenVerifier
}

func (m *msMultiTenantVerifier) Verify(ctx context.Context, rawIDToken string) (*oidc.IDToken, error) {
	token, err := m.underlying.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(token.Issuer, "https://login.microsoftonline.com/") || !strings.HasSuffix(token.Issuer, "/v2.0") {
		return nil, fmt.Errorf("oidc: invalid issuer %q for Microsoft multi-tenant provider", token.Issuer)
	}
	return token, nil
}

// InitOIDCRegistry parses JSON configuration and initializes all OIDC providers via remote discovery.
func InitOIDCRegistry(ctx context.Context, configJSON string) (*OIDCRegistry, error) {
	if strings.TrimSpace(configJSON) == "" {
		return nil, fmt.Errorf("OIDC configuration JSON is empty")
	}

	var payload OIDCConfigPayload
	if err := json.Unmarshal([]byte(configJSON), &payload); err != nil {
		return nil, fmt.Errorf("failed to parse OIDC configuration JSON: %w", err)
	}

	if len(payload.AuthProviders) == 0 {
		return nil, fmt.Errorf("OIDC configuration contains no auth_providers")
	}

	registry := &OIDCRegistry{}
	for _, p := range payload.AuthProviders {
		clientID := p.ClientID
		if p.ClientIDName != "" {
			if envVal := config.Config().GetConfigString(p.ClientIDName); envVal != "" {
				clientID = envVal
			}
		}

		if p.ID == "" || p.Issuer == "" || clientID == "" {
			return nil, fmt.Errorf("invalid provider config: id, issuer, and client_id (or valid client_id_name) are required (got id=%q, issuer=%q)", p.ID, p.Issuer)
		}
		p.ClientID = clientID

		clientSecret := p.ClientSecret
		if p.SecretName != "" && clientSecret == "" {
			if envVal := config.Config().GetConfigString(p.SecretName); envVal != "" {
				clientSecret = envVal
			}
		}

		regP := RegisteredProvider{
			Config:       p,
			ClientSecret: clientSecret,
			initOnce:     new(sync.Once),
		}

		// If static endpoints are specified, initialize immediately with zero network calls.
		// Otherwise, defer OIDC discovery lazily until first use.
		if p.AuthURL != "" && p.TokenURL != "" && p.JWKSURL != "" {
			if err := regP.initProvider(ctx); err != nil {
				return nil, err
			}
			slog.Info("Successfully registered static OIDC provider (zero WAN calls)", "id", p.ID, "issuer", p.Issuer, "client_id", p.ClientID)
		} else {
			slog.Info("Successfully registered lazy OIDC provider (discovery deferred)", "id", p.ID, "issuer", p.Issuer, "client_id", p.ClientID)
		}

		registry.Providers = append(registry.Providers, regP)
	}

	return registry, nil
}
