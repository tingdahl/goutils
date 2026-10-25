package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tingdahl/goutils/jwt"
	"github.com/tingdahl/goutils/telemetry"
)

const (
	// TokenTypUserSession is the typ claim for user application session tokens.
	TokenTypUserSession = "user_session"

	// DefaultUserSessionDuration is the standard validity for an active user session token (12 hours).
	DefaultUserSessionDuration = 12 * time.Hour

	// DefaultUserRenewalThreshold is the remaining TTL window during which active requests trigger rolling renewal (4 hours).
	DefaultUserRenewalThreshold = 4 * time.Hour

	// HeaderRenewedToken is the HTTP response header delivering rolling renewal tokens.
	HeaderRenewedToken = "X-Renewed-Token"
)

// UserSessionClaims defines claims embedded in an application session bearer token.
type UserSessionClaims struct {
	Typ         string `json:"typ"` // "user_session"
	Sub         string `json:"sub"` // User ID / UUID
	Email       string `json:"email,omitempty"`
	Name        string `json:"name,omitempty"`
	Provider    string `json:"provider,omitempty"`
	AccountType string `json:"account_type,omitempty"`
	Iat         int64  `json:"iat"`
	Exp         int64  `json:"exp"`
}

type userSessionClaimsKey struct{}

// WithUserSessionClaims injects UserSessionClaims into the context.
func WithUserSessionClaims(ctx context.Context, claims *UserSessionClaims) context.Context {
	return context.WithValue(ctx, userSessionClaimsKey{}, claims)
}

// GetUserSessionClaims extracts UserSessionClaims from context if present.
func GetUserSessionClaims(ctx context.Context) (*UserSessionClaims, bool) {
	claims, ok := ctx.Value(userSessionClaimsKey{}).(*UserSessionClaims)
	return claims, ok
}

// SessionTokenManager manages generation and verification of user session tokens.
type SessionTokenManager struct {
	Secret           string
	SessionDuration  time.Duration
	RenewalThreshold time.Duration
}

// NewSessionTokenManager initializes a SessionTokenManager with default durations.
func NewSessionTokenManager(secret string) *SessionTokenManager {
	return &SessionTokenManager{
		Secret:           secret,
		SessionDuration:  DefaultUserSessionDuration,
		RenewalThreshold: DefaultUserRenewalThreshold,
	}
}

// MintUserSessionToken creates a signed user application session JWT valid for SessionDuration.
func (tm *SessionTokenManager) MintUserSessionToken(userID, email, name, provider, accountType string) (string, error) {
	duration := tm.SessionDuration
	if duration <= 0 {
		duration = DefaultUserSessionDuration
	}
	return tm.MintUserSessionTokenWithTTL(userID, email, name, provider, accountType, duration)
}

// MintUserSessionTokenWithTTL creates a signed user session JWT valid for the specified TTL.
func (tm *SessionTokenManager) MintUserSessionTokenWithTTL(userID, email, name, provider, accountType string, ttl time.Duration) (string, error) {
	if tm.Secret == "" {
		return "", jwt.ErrMissingSecret
	}
	now := time.Now()
	claims := UserSessionClaims{
		Typ:         TokenTypUserSession,
		Sub:         strings.TrimSpace(userID),
		Email:       strings.TrimSpace(email),
		Name:        strings.TrimSpace(name),
		Provider:    strings.TrimSpace(provider),
		AccountType: strings.TrimSpace(accountType),
		Iat:         now.Unix(),
		Exp:         now.Add(ttl).Unix(),
	}
	return jwt.Sign(claims, tm.Secret)
}

// VerifyUserSessionToken validates a session JWT, checks signature and expiration, and extracts claims.
func (tm *SessionTokenManager) VerifyUserSessionToken(tokenStr string) (*UserSessionClaims, error) {
	if tm.Secret == "" {
		return nil, jwt.ErrMissingSecret
	}
	var claims UserSessionClaims
	if err := jwt.Verify(tokenStr, tm.Secret, &claims); err != nil {
		return nil, err
	}
	if claims.Typ != TokenTypUserSession {
		return nil, fmt.Errorf("%w: unexpected token type %q", jwt.ErrInvalidToken, claims.Typ)
	}
	return &claims, nil
}

// BuildSessionRedirect constructs a redirect URL with the session token in the URL fragment (#token=...).
// Delivering tokens in the hash fragment ensures tokens are never logged in server access logs or leaked via Referer headers.
func BuildSessionRedirect(targetURL, sessionToken string) string {
	cleanTarget := targetURL
	if idx := strings.Index(cleanTarget, "#"); idx != -1 {
		cleanTarget = cleanTarget[:idx]
	}
	return fmt.Sprintf("%s#token=%s", cleanTarget, url.QueryEscape(sessionToken))
}

// BuildAuthErrorRedirect constructs a redirect URL conveying error codes in query params
// and identity details (provider, accountType, email) in the URL fragment to prevent log leakage.
func BuildAuthErrorRedirect(targetURL, errorCode, provider, accountType, email string) string {
	cleanTarget := targetURL
	if idx := strings.Index(cleanTarget, "#"); idx != -1 {
		cleanTarget = cleanTarget[:idx]
	}

	sep := "?"
	if strings.Contains(cleanTarget, "?") {
		sep = "&"
	}
	targetWithQuery := fmt.Sprintf("%s%serror=%s", cleanTarget, sep, url.QueryEscape(errorCode))

	fragParams := url.Values{}
	if provider != "" {
		fragParams.Set("error_provider", provider)
	}
	if accountType != "" && accountType != AccountTypeNA {
		fragParams.Set("error_account_type", accountType)
	}
	if email != "" {
		fragParams.Set("error_email", email)
	}

	if len(fragParams) > 0 {
		return fmt.Sprintf("%s#%s", targetWithQuery, fragParams.Encode())
	}
	return targetWithQuery
}

// SessionUserResolver resolves application-specific user data from UserSessionClaims.
type SessionUserResolver interface {
	ResolveSessionUser(ctx context.Context, claims *UserSessionClaims) (context.Context, error)
}

// SessionUserResolverFunc adapter for functions as SessionUserResolver.
type SessionUserResolverFunc func(ctx context.Context, claims *UserSessionClaims) (context.Context, error)

// ResolveSessionUser invokes the underlying function.
func (f SessionUserResolverFunc) ResolveSessionUser(ctx context.Context, claims *UserSessionClaims) (context.Context, error) {
	return f(ctx, claims)
}

// SessionAuthMiddleware creates HTTP middleware requiring a valid application session token.
// Supports active rolling renewal via X-Renewed-Token header when the token enters the renewal threshold.
func SessionAuthMiddleware(tm *SessionTokenManager, resolver SessionUserResolver, logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get(AuthorizationHeader)
			if authHeader == "" || !strings.HasPrefix(authHeader, BearerPrefix) {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			tokenStr := strings.TrimPrefix(authHeader, BearerPrefix)
			claims, err := tm.VerifyUserSessionToken(tokenStr)
			if err != nil {
				if errors.Is(err, jwt.ErrTokenExpired) {
					logger.WarnContext(r.Context(), "User session token has expired", "error", err)
					http.Error(w, "Unauthorized: Session expired", http.StatusUnauthorized)
					return
				}
				logger.WarnContext(r.Context(), "Invalid session token", "error", err)
				http.Error(w, "Unauthorized: Invalid Token", http.StatusUnauthorized)
				return
			}

			// Active rolling renewal: if token is nearing expiration, issue an updated token
			remaining := time.Until(time.Unix(claims.Exp, 0))
			threshold := tm.RenewalThreshold
			if threshold <= 0 {
				threshold = DefaultUserRenewalThreshold
			}
			if remaining > 0 && remaining <= threshold {
				renewedToken, err := tm.MintUserSessionToken(claims.Sub, claims.Email, claims.Name, claims.Provider, claims.AccountType)
				if err == nil {
					w.Header().Set(HeaderRenewedToken, renewedToken)
					w.Header().Set("Access-Control-Expose-Headers", HeaderRenewedToken)
				}
			}

			ctx := r.Context()
			ctx = WithUserSessionClaims(ctx, claims)
			ctx = WithSessionInfo(ctx, SessionInfo{
				Provider:    claims.Provider,
				AccountType: claims.AccountType,
			})
			ctx = telemetry.WithUserMetadata(ctx, claims.Sub, claims.Email, claims.Name, claims.Provider, claims.Sub)

			if resolver != nil {
				enrichedCtx, err := resolver.ResolveSessionUser(ctx, claims)
				if err != nil {
					logger.WarnContext(ctx, "Session user resolution failed", "error", err, "user_id", claims.Sub)
					http.Error(w, "Forbidden: User resolution failed", http.StatusForbidden)
					return
				}
				ctx = enrichedCtx
			}

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
