package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/tingdahl/goutils/config"
)

// AuthProviderConfig holds static configuration for an OIDC identity provider.
type AuthProviderConfig struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Icon         string   `json:"icon,omitempty"`
	Issuer       string   `json:"issuer"`
	ClientID     string   `json:"client_id"`
	SecretName   string   `json:"secret_name,omitempty"`
	ClientSecret string   `json:"client_secret,omitempty"`
	RedirectPath string   `json:"redirect_path,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`
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
		if p.ID == "" || p.Issuer == "" || p.ClientID == "" {
			return nil, fmt.Errorf("invalid provider config: id, issuer, and client_id are required (got id=%q, issuer=%q)", p.ID, p.Issuer)
		}

		provider, err := oidc.NewProvider(ctx, p.Issuer)
		if err != nil {
			return nil, fmt.Errorf("error initializing OIDC provider %q (%s): %w", p.ID, p.Issuer, err)
		}

		verifier := provider.Verifier(&oidc.Config{ClientID: p.ClientID})
		clientSecret := p.ClientSecret

		if p.SecretName != "" && clientSecret == "" {
			if envVal := config.Config().GetConfigString(p.SecretName); envVal != "" {
				clientSecret = envVal
			}
		}

		regP := RegisteredProvider{
			Config:       p,
			Provider:     provider,
			Verifier:     verifier,
			ClientSecret: clientSecret,
		}
		registry.Providers = append(registry.Providers, regP)
		slog.Info("Successfully registered OIDC provider", "id", p.ID, "issuer", p.Issuer, "client_id", p.ClientID)
	}

	return registry, nil
}
