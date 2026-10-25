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
// The user ID (sub) is the sole identity key; user profile data (email, name, roles) is
// authoritatively retrieved from the database/storage by the user resolver on each request.
type UserSessionClaims struct {
	Typ      string `json:"typ"`                 // "user_session"
	Sub      string `json:"sub"`                 // User ID / UUID
	Provider string `json:"provider,omitempty"` // e.g. "google", "microsoft"
	Iat      int64  `json:"iat"`
	Exp      int64  `json:"exp"`
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
func (tm *SessionTokenManager) MintUserSessionToken(userID, provider string) (string, error) {
	return tm.MintUserSessionTokenWithTTL(userID, provider, tm.SessionDuration)
}

// MintUserSessionTokenWithTTL creates a signed user session JWT valid for the specified TTL.
func (tm *SessionTokenManager) MintUserSessionTokenWithTTL(userID, provider string, ttl time.Duration) (string, error) {
	if tm.Secret == "" {
		return "", jwt.ErrMissingSecret
	}
	now := time.Now()
	claims := UserSessionClaims{
		Typ:      TokenTypUserSession,
		Sub:      strings.TrimSpace(userID),
		Provider: strings.TrimSpace(provider),
		Iat:      now.Unix(),
		Exp:      now.Add(ttl).Unix(),
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
	return fmt.Sprintf("%s#token=%s", targetURL, sessionToken)
}

// BuildAuthErrorRedirect constructs a redirect URL conveying error details strictly in the URL fragment.
// Conveying error details in the fragment prevents sensitive parameters (such as email, provider)
// from ever appearing in server access logs, CDN logs, or leaking through Referer headers.
func BuildAuthErrorRedirect(targetURL, errorCode, provider, email string) string {
	fragParams := url.Values{}
	fragParams.Set("error", errorCode)
	if provider != "" {
		fragParams.Set("error_provider", provider)
	}
	if email != "" {
		fragParams.Set("error_email", email)
	}
	return fmt.Sprintf("%s#%s", targetURL, fragParams.Encode())
}

// SessionUserResolver resolves application-specific user data from UserSessionClaims.
type SessionUserResolver interface {
	ResolveSessionUser(ctx context.Context, claims *UserSessionClaims) (context.Context, error)
}

// SessionAuthMiddleware creates HTTP middleware requiring a valid application session token.
// Supports active rolling renewal via X-Renewed-Token header when the token enters the renewal threshold.
func SessionAuthMiddleware(tm *SessionTokenManager, resolver SessionUserResolver, logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	if resolver == nil {
		panic("auth: resolver cannot be nil in SessionAuthMiddleware")
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
			if remaining > 0 && remaining <= tm.RenewalThreshold {
				renewedToken, err := tm.MintUserSessionToken(claims.Sub, claims.Provider)
				if err == nil {
					w.Header().Set(HeaderRenewedToken, renewedToken)
					w.Header().Set("Access-Control-Expose-Headers", HeaderRenewedToken)
				}
			}

			ctx := r.Context()
			ctx = WithSessionInfo(ctx, SessionInfo{
				Provider: claims.Provider,
			})

			enrichedCtx, err := resolver.ResolveSessionUser(ctx, claims)
			if err != nil {
				logger.WarnContext(ctx, "Session user resolution failed", "error", err, "user_id", claims.Sub)
				http.Error(w, "Forbidden: User resolution failed", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r.WithContext(enrichedCtx))
		})
	}
}
