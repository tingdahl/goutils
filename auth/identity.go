package auth

import (
	"context"
	"errors"
)

// Provider IDs for standard OIDC integrations.
const (
	MicrosoftProviderID = "microsoft"
	ProviderMicrosoft   = "microsoft"
	ProviderGoogle      = "google"

	// MicrosoftConsumerTenantID is the tenant ID of personal Microsoft accounts (MSA),
	// used for email trust verification in claims.go.
	MicrosoftConsumerTenantID = "9188040d-6c67-4c5b-b112-36a304b66dad"
)

type identityContextKey struct{}

// UserIdentity represents a verified identity extracted from an OIDC ID token.
// The Subject field is normalized at parse time:
//   - Google: google sub
//   - Microsoft: tid:oid
type UserIdentity struct {
	ProviderID    string
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
	Issuer        string
	Claims        map[string]interface{}
}

// StableSubject returns the canonical subject identifier (identical to Subject).
func (u UserIdentity) StableSubject() (string, error) {
	if u.Subject == "" {
		return "", errors.New("auth: missing subject")
	}
	return u.Subject, nil
}

// StableSubject returns the canonical subject identifier (identical to identity.Subject).
func StableSubject(identity UserIdentity) (string, error) {
	if identity.Subject == "" {
		return "", errors.New("auth: missing subject")
	}
	return identity.Subject, nil
}

// WithUserIdentity injects UserIdentity into the context.
func WithUserIdentity(ctx context.Context, identity UserIdentity) context.Context {
	return context.WithValue(ctx, identityContextKey{}, identity)
}

// GetUserIdentity extracts UserIdentity from context if present.
func GetUserIdentity(ctx context.Context) (UserIdentity, bool) {
	identity, ok := ctx.Value(identityContextKey{}).(UserIdentity)
	return identity, ok
}

type sessionInfoKey struct{}

// SessionInfo holds metadata about an authenticated user session.
type SessionInfo struct {
	Provider string
}

// WithSessionInfo injects SessionInfo into context.
func WithSessionInfo(ctx context.Context, info SessionInfo) context.Context {
	return context.WithValue(ctx, sessionInfoKey{}, info)
}

// GetSessionInfo extracts SessionInfo from context if present.
func GetSessionInfo(ctx context.Context) (SessionInfo, bool) {
	info, ok := ctx.Value(sessionInfoKey{}).(SessionInfo)
	return info, ok
}

// IdentityResolver resolves application-specific identity data (e.g. database user, roles, permissions)
// from a verified UserIdentity and enriches the context.
type IdentityResolver interface {
	ResolveIdentity(ctx context.Context, identity UserIdentity) (context.Context, error)
}

// IdentityResolverFunc is an adapter to allow the use of ordinary functions as IdentityResolver.
type IdentityResolverFunc func(ctx context.Context, identity UserIdentity) (context.Context, error)

// ResolveIdentity invokes the underlying function.
func (f IdentityResolverFunc) ResolveIdentity(ctx context.Context, identity UserIdentity) (context.Context, error) {
	return f(ctx, identity)
}
