package auth

import (
	"context"
)

type identityContextKey struct{}

// UserIdentity represents a verified identity extracted from an OIDC ID token.
type UserIdentity struct {
	ProviderID    string
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
	Issuer        string
	Claims        map[string]interface{}
}

// IdentityResolver resolves application-specific identity data (e.g. database user, roles, permissions)
// from a verified UserIdentity and enriches the context.
type IdentityResolver interface {
	ResolveIdentity(ctx context.Context, identity UserIdentity) (context.Context, error)
}

// IdentityResolverFunc is an adapter to allow the use of ordinary functions as IdentityResolver.
type IdentityResolverFunc func(ctx context.Context, identity UserIdentity) (context.Context, error)

// ResolveIdentity calls f(ctx, identity).
func (f IdentityResolverFunc) ResolveIdentity(ctx context.Context, identity UserIdentity) (context.Context, error) {
	return f(ctx, identity)
}

// WithUserIdentity injects a UserIdentity into the context.
func WithUserIdentity(ctx context.Context, id UserIdentity) context.Context {
	return context.WithValue(ctx, identityContextKey{}, id)
}

// GetUserIdentity extracts the UserIdentity from the context if present.
func GetUserIdentity(ctx context.Context) (UserIdentity, bool) {
	id, ok := ctx.Value(identityContextKey{}).(UserIdentity)
	return id, ok
}
