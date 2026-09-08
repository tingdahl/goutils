package auth

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/tingdahl/goutils/telemetry"
)

const (
	AuthorizationHeader = "Authorization"
	BearerPrefix        = "Bearer "
)

// AuthMiddleware creates HTTP middleware requiring a valid OIDC Bearer token.
// The token is verified against all registered providers in registry, claims are extracted,
// and the provided IdentityResolver is invoked to enrich context with application-specific user state.
func AuthMiddleware(registry *OIDCRegistry, resolver IdentityResolver, logger *slog.Logger) func(http.Handler) http.Handler {
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

			idTokenString := strings.TrimPrefix(authHeader, BearerPrefix)
			if registry == nil || len(registry.Providers) == 0 {
				logger.ErrorContext(r.Context(), "No OIDC providers registered")
				http.Error(w, "Unauthorized: OIDC authentication not configured", http.StatusUnauthorized)
				return
			}

			var verifiedToken *oidc.IDToken
			var matchedProvider *RegisteredProvider

			for i := range registry.Providers {
				p := &registry.Providers[i]
				if p.Verifier == nil {
					continue
				}
				token, err := p.Verifier.Verify(r.Context(), idTokenString)
				if err == nil {
					verifiedToken = token
					matchedProvider = p
					break
				}
			}

			if verifiedToken == nil {
				logger.WarnContext(r.Context(), "Token verification failed against all configured providers")
				http.Error(w, "Unauthorized: Invalid Token", http.StatusUnauthorized)
				return
			}

			var claims struct {
				Email         string `json:"email"`
				EmailVerified *bool  `json:"email_verified"`
				Name          string `json:"name"`
			}
			if err := verifiedToken.Claims(&claims); err != nil {
				logger.ErrorContext(r.Context(), "Failed to parse claims from ID token", "error", err)
				http.Error(w, "Unauthorized: Invalid Claims", http.StatusUnauthorized)
				return
			}

			var rawClaims map[string]interface{}
			_ = verifiedToken.Claims(&rawClaims)

			emailVerified := false
			if claims.EmailVerified != nil {
				emailVerified = *claims.EmailVerified
			}

			identity := UserIdentity{
				ProviderID:    matchedProvider.Config.ID,
				Subject:       verifiedToken.Subject,
				Email:         claims.Email,
				EmailVerified: emailVerified,
				Name:          claims.Name,
				Issuer:        verifiedToken.Issuer,
				Claims:        rawClaims,
			}

			ctx := r.Context()
			ctx = WithUserIdentity(ctx, identity)
			ctx = telemetry.WithUserMetadata(ctx, identity.Subject, identity.Email, identity.Name, identity.Issuer, identity.Subject)

			if resolver != nil {
				enrichedCtx, err := resolver.ResolveIdentity(ctx, identity)
				if err != nil {
					logger.WarnContext(ctx, "Identity resolution failed or rejected user", "error", err, "provider", identity.ProviderID, "email", identity.Email)
					http.Error(w, "Forbidden: User resolution failed", http.StatusForbidden)
					return
				}
				ctx = enrichedCtx
			}

			logger.InfoContext(ctx, "User authenticated", "provider", identity.ProviderID, "email", identity.Email)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
