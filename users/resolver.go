package users

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tingdahl/goutils/auth"
	"github.com/tingdahl/goutils/config"
	"github.com/tingdahl/goutils/telemetry"
)

type userContextKey struct{}

// WithUser adds the authenticated User to the context.
func WithUser(ctx context.Context, u *UserProtoRow) context.Context {
	return context.WithValue(ctx, userContextKey{}, u)
}

// GetUser extracts the authenticated User from the context if present.
func GetUser(ctx context.Context) (*UserProtoRow, bool) {
	u, ok := ctx.Value(userContextKey{}).(*UserProtoRow)
	if ok && u != nil {
		return u, true
	}
	return nil, false
}

// IsSiteAdmin checks if the user's email or ID is configured as a site administrator in SITE_ADMINS.
// The user must also have at least one linked OIDC identity.
func IsSiteAdmin(u *UserProtoRow) bool {
	if u == nil || !u.HasLinkedIdentity() {
		return false
	}
	return IsEmailSiteAdmin(u.Email) || IsEmailSiteAdmin(u.Id)
}

// IsEmailSiteAdmin checks if the given email or identifier is configured in SITE_ADMINS.
func IsEmailSiteAdmin(emailOrID string) bool {
	emailOrID = strings.Trim(strings.TrimSpace(emailOrID), "\"'`")
	if emailOrID == "" {
		return false
	}
	adminsRaw := config.Config().GetConfigString("SITE_ADMINS")
	if adminsRaw == "" {
		return false
	}
	for _, admin := range strings.Split(adminsRaw, ",") {
		admin = strings.Trim(strings.TrimSpace(admin), "\"'`")
		if admin == "" {
			continue
		}
		if strings.EqualFold(emailOrID, admin) {
			return true
		}
	}
	return false
}

// IsPublicSignupAllowed checks if public signup is permitted via ALLOW_PUBLIC_SIGNUP (defaults to false).
func IsPublicSignupAllowed() bool {
	val := config.Config().GetConfigString("ALLOW_PUBLIC_SIGNUP")
	val = strings.Trim(strings.TrimSpace(strings.ToLower(val)), "\"'`")
	return val == "true" || val == "1" || val == "yes"
}

var (
	ErrPublicSignupDisabled = errors.New("auth: public registration is disabled; invited users only")
	ErrIdentityConflict     = errors.New("auth: account already linked to a different identity for this provider")
	ErrEmailNotVerified     = errors.New("auth: email is not verified by the identity provider")
)

func hashSubject12(sub string) string {
	h := sha256.Sum256([]byte(sub))
	return hex.EncodeToString(h[:6])
}

func emailDomain(email string) string {
	if idx := strings.LastIndex(email, "@"); idx != -1 {
		return strings.ToLower(email[idx+1:])
	}
	return ""
}

// IdentityResolver resolves OIDC tokens and user sessions against UsersClient.
type IdentityResolver struct {
	UsersClient       *UsersClient
	AllowPublicSignup *bool
	IsSiteAdminFn     func(emailOrID string) bool
}

// NewIdentityResolver creates an IdentityResolver instance for a UsersClient.
func NewIdentityResolver(uc *UsersClient) *IdentityResolver {
	return &IdentityResolver{
		UsersClient: uc,
	}
}

// ResolveSessionUser implements auth.SessionUserResolver for application session tokens.
func (r *IdentityResolver) ResolveSessionUser(ctx context.Context, claims *auth.UserSessionClaims) (context.Context, error) {
	if claims == nil || claims.Sub == "" {
		return nil, errors.New("auth: session claims missing sub")
	}

	user := r.UsersClient.GetUserByID(claims.Sub)
	if user == nil {
		return nil, fmt.Errorf("auth: user not found for session sub: %s", claims.Sub)
	}

	ctx = WithUser(ctx, user)
	ctx = telemetry.WithUserMetadata(ctx, user.Id, user.Email, user.Name, claims.Provider, user.Id)
	return ctx, nil
}

// ResolveIdentity maps verified OIDC identity claims into application User models.
func (r *IdentityResolver) ResolveIdentity(ctx context.Context, identity auth.UserIdentity) (context.Context, error) {
	return r.ResolveIdentityAllowingSignup(ctx, identity, false)
}

// ResolveIdentityAllowingSignup resolves the user exactly like ResolveIdentity.
func (r *IdentityResolver) ResolveIdentityAllowingSignup(ctx context.Context, identity auth.UserIdentity, allowSignup bool) (context.Context, error) {
	email := strings.TrimSpace(strings.ToLower(identity.Email))
	if email == "" {
		return nil, errors.New("auth: ID token missing required email claim")
	}

	domain := emailDomain(email)

	if !identity.EmailVerified {
		whichRule := "missing xms_edov and not consumer tenant"
		if identity.ProviderID != auth.ProviderMicrosoft {
			whichRule = "email_verified claim false or missing"
		}
		slog.WarnContext(ctx, "ResolveIdentity: email is not verified by provider",
			"provider", identity.ProviderID,
			"subject", identity.Subject,
			"email_domain", domain,
			"rule_failed", whichRule,
		)
		return nil, fmt.Errorf("%w: provider %q", ErrEmailNotVerified, identity.ProviderID)
	}

	subject, err := identity.StableSubject()
	if err != nil {
		return nil, fmt.Errorf("auth: invalid identity subject: %w", err)
	}
	providerID := identity.ProviderID

	// 1. Try finding existing user by linked OIDC identity
	user := r.UsersClient.GetUserByIdentity(providerID, subject)
	if user != nil {
		if identity.Name != "" && user.Name != identity.Name {
			_ = r.UsersClient.UpdateUserName(ctx, user.Id, identity.Name)
			user.Name = identity.Name
		}
		return WithUser(ctx, user), nil
	}

	// 2. Try finding existing user by verified email
	user = r.UsersClient.GetUserByEmail(email)
	if user != nil {
		if existing := user.Identities[providerID]; existing != "" && existing != subject {
			var linkedProviders []string
			for p := range user.Identities {
				linkedProviders = append(linkedProviders, p)
			}
			sort.Strings(linkedProviders)

			slog.WarnContext(ctx, "ResolveIdentity: refusing to link identity; account already linked to another subject for this provider",
				"user_id", user.Id,
				"provider", providerID,
				"subject", subject,
				"email_domain", domain,
				"presented_subject_hash", hashSubject12(subject),
				"linked_subject_hash", hashSubject12(existing),
				"linked_providers", linkedProviders,
				"created_at", time.UnixMilli(user.CreatedAtUnixMs).UTC().Format(time.RFC3339),
				"updated_at", time.UnixMilli(user.UpdatedAtUnixMs).UTC().Format(time.RFC3339),
			)
			return nil, ErrIdentityConflict
		}
		if err := r.UsersClient.LinkUserIdentity(ctx, user.Id, providerID, subject); err != nil {
			if errors.Is(err, ErrIdentityAlreadyLinked) {
				return nil, ErrIdentityConflict
			}
			return nil, fmt.Errorf("auth: failed to link identity: %w", err)
		}
		slog.InfoContext(ctx, "ResolveIdentity: linked identity to existing account by verified email",
			"user_id", user.Id,
			"provider", providerID,
			"issuer", identity.Issuer,
		)
		if identity.Name != "" && user.Name != identity.Name {
			_ = r.UsersClient.UpdateUserName(ctx, user.Id, identity.Name)
		}
		refreshed := r.UsersClient.GetUserByID(user.Id)
		if refreshed != nil {
			user = refreshed
		}
		return WithUser(ctx, user), nil
	}

	// 3. New user registration check
	isSiteAdmin := false
	if r.IsSiteAdminFn != nil {
		isSiteAdmin = r.IsSiteAdminFn(email)
	} else {
		isSiteAdmin = IsEmailSiteAdmin(email)
	}

	publicSignup := false
	if r.AllowPublicSignup != nil {
		publicSignup = *r.AllowPublicSignup
	} else {
		publicSignup = IsPublicSignupAllowed()
	}

	if !publicSignup && !isSiteAdmin && !allowSignup {
		slog.WarnContext(ctx, "ResolveIdentity: public registration disabled and user is not a site admin",
			"email_domain", domain,
			"is_site_admin", isSiteAdmin,
			"allow_public_signup", publicSignup,
		)
		return nil, ErrPublicSignupDisabled
	}
	if isSiteAdmin {
		slog.InfoContext(ctx, "ResolveIdentity: permitting new registration for site admin",
			"allow_public_signup", publicSignup,
		)
	}

	// 4. Create new user
	userID := uuid.NewString()
	name := identity.Name
	if name == "" {
		name = strings.Split(email, "@")[0]
	}

	createdUser, err := r.UsersClient.AddUserWithIdentity(ctx, email, userID, name, providerID, subject)
	if err != nil {
		return nil, fmt.Errorf("auth: failed to create new user: %w", err)
	}

	return WithUser(ctx, createdUser), nil
}
