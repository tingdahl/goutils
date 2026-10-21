package auth

import (
	"context"
	"errors"
	"strings"
)

// Provider IDs for standard OIDC integrations.
const (
	MicrosoftProviderID = "microsoft"
	ProviderMicrosoft   = "microsoft"
	ProviderGoogle      = "google"

	AccountTypePersonal = "personal"
	AccountTypeWork     = "work"
	AccountTypeNA       = "n.a."

	// MicrosoftConsumerTenantID is the tenant ID of personal Microsoft accounts (MSA).
	MicrosoftConsumerTenantID = "9188040d-6c67-4c5b-b112-36a304b66dad"
)

// ErrMissingStableIdentifier is returned when an identity lacks required claims for a stable subject identifier.
var ErrMissingStableIdentifier = errors.New("auth: missing stable identifier claims")

type identityContextKey struct{}

// UserIdentity represents a verified identity extracted from an OIDC ID token.
type UserIdentity struct {
	ProviderID    string
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
	Issuer        string
	TenantID      string // OIDC "tid" claim
	ObjectID      string // OIDC "oid" claim
	Claims        map[string]interface{}
}

// StableSubject returns the canonical stable subject identifier for account linking.
// For Microsoft, this is tid:oid. If either is missing, it returns an error.
// For other providers, this returns Subject. If Subject is empty, it returns an error.
func (u UserIdentity) StableSubject() (string, error) {
	if u.ProviderID == MicrosoftProviderID || u.ProviderID == ProviderMicrosoft {
		tid := strings.TrimSpace(u.GetTenantID())
		oid := strings.TrimSpace(u.GetObjectID())
		if tid == "" || oid == "" {
			return "", ErrMissingStableIdentifier
		}
		return tid + ":" + oid, nil
	}
	sub := strings.TrimSpace(u.Subject)
	if sub == "" {
		return "", ErrMissingStableIdentifier
	}
	return sub, nil
}

// GetTenantID extracts the tenant ID from the identity or its claims.
func (u UserIdentity) GetTenantID() string {
	if u.TenantID != "" {
		return u.TenantID
	}
	if tid, ok := u.Claims[ClaimTenantID].(string); ok {
		return strings.TrimSpace(tid)
	}
	return ""
}

// GetObjectID extracts the object ID from the identity or its claims.
func (u UserIdentity) GetObjectID() string {
	if u.ObjectID != "" {
		return u.ObjectID
	}
	if oid, ok := u.Claims[ClaimObjectID].(string); ok {
		return strings.TrimSpace(oid)
	}
	return ""
}

// AccountType returns "personal", "work", or "n.a." based on the identity's provider and tenant.
func (u UserIdentity) AccountType() string {
	if u.ProviderID == MicrosoftProviderID || u.ProviderID == ProviderMicrosoft {
		tid := u.GetTenantID()
		if strings.EqualFold(strings.TrimSpace(tid), MicrosoftConsumerTenantID) {
			return AccountTypePersonal
		}
		return AccountTypeWork
	}
	return AccountTypeNA
}

// GetTenantID extracts the tenant ID from the identity or its claims.
func GetTenantID(identity UserIdentity) string {
	return identity.GetTenantID()
}

// GetObjectID extracts the object ID from the identity or its claims.
func GetObjectID(identity UserIdentity) string {
	return identity.GetObjectID()
}

// AccountTypeForIdentity returns "personal", "work", or "n.a." based on the identity's provider and tenant.
func AccountTypeForIdentity(identity UserIdentity) string {
	return identity.AccountType()
}

// StableSubject returns the canonical stable subject identifier for account linking.
func StableSubject(identity UserIdentity) (string, error) {
	return identity.StableSubject()
}

type sessionInfoKey struct{}

// SessionInfo holds metadata about an authenticated user session (such as provider and account type).
type SessionInfo struct {
	Provider    string
	AccountType string
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
