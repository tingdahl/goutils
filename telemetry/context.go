package telemetry

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
)

type contextKeyType string

const (
	traceContextKey      contextKeyType = "trace_id"
	sessionContextKey    contextKeyType = "session_id"
	userIDContextKey     contextKeyType = "user_id"
	emailContextKey      contextKeyType = "email"
	userNameContextKey   contextKeyType = "user_name"
	oidcIssuerContextKey contextKeyType = "oidc_issuer"
	oidcSubContextKey    contextKeyType = "oidc_sub"
	clientIPContextKey   contextKeyType = "client_ip"
	userAgentContextKey  contextKeyType = "user_agent"
)

const (
	LogKeyTraceID   = "trace_id"
	LogKeySessionID = "session_id"
	LogKeyUserID    = "user_id"
	LogKeyEmail     = "email"
	LogKeyClientIP  = "client_ip"
	LogKeyUserAgent = "user_agent"
)

const (
	HeaderTraceID   = "Trace-ID"
	HeaderSessionID = "X-Session-ID"
	CookieSessionID = "session_id"
	HeaderGCPTrace  = "X-Cloud-Trace-Context"
)

// ContextHandler is a slog.Handler that pulls observability keys from context
// and adds them as structured attributes to log records.
type ContextHandler struct {
	Next slog.Handler
}

// Enabled reports whether the handler handles records at the given level.
func (h *ContextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.Next.Enabled(ctx, level)
}

// Handle adds context attributes (trace_id, session_id, user_id, email, client_ip, user_agent)
// to the log record before delegating to Next.
func (h *ContextHandler) Handle(ctx context.Context, r slog.Record) error {
	if traceID, ok := ctx.Value(traceContextKey).(string); ok && traceID != "" {
		r.AddAttrs(slog.String(LogKeyTraceID, traceID))
	}
	if sessionID, ok := ctx.Value(sessionContextKey).(string); ok && sessionID != "" {
		r.AddAttrs(slog.String(LogKeySessionID, sessionID))
	}
	if userID, ok := ctx.Value(userIDContextKey).(string); ok && userID != "" {
		r.AddAttrs(slog.String(LogKeyUserID, userID))
	}
	if email, ok := ctx.Value(emailContextKey).(string); ok && email != "" {
		r.AddAttrs(slog.String(LogKeyEmail, email))
	}
	if clientIP, ok := ctx.Value(clientIPContextKey).(string); ok && clientIP != "" {
		r.AddAttrs(slog.String(LogKeyClientIP, clientIP))
	}
	if userAgent, ok := ctx.Value(userAgentContextKey).(string); ok && userAgent != "" {
		r.AddAttrs(slog.String(LogKeyUserAgent, userAgent))
	}
	return h.Next.Handle(ctx, r)
}

// WithAttrs returns a new ContextHandler whose attributes consist of h's attributes followed by attrs.
func (h *ContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &ContextHandler{Next: h.Next.WithAttrs(attrs)}
}

// WithGroup returns a new ContextHandler with the given group appended.
func (h *ContextHandler) WithGroup(name string) slog.Handler {
	return &ContextHandler{Next: h.Next.WithGroup(name)}
}

// TraceMiddleware ensures every request has a Trace-ID, Session-ID, Client IP, and User-Agent injected into the context.
func TraceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handle Trace ID
		traceID := GetTraceIDFromRequest(r)
		if traceID == "" {
			traceID = uuid.New().String()
		}

		// Handle Session ID
		sessionID := GetSessionIDFromRequest(r)
		if sessionID == "" {
			sessionID = uuid.New().String()
			SetSessionCookie(w, sessionID)
		}

		clientIP := GetClientIP(r)
		userAgent := r.UserAgent()

		ctx := r.Context()
		ctx = context.WithValue(ctx, traceContextKey, traceID)
		ctx = context.WithValue(ctx, sessionContextKey, sessionID)
		ctx = context.WithValue(ctx, clientIPContextKey, clientIP)
		ctx = context.WithValue(ctx, userAgentContextKey, userAgent)

		SetTraceIDInResponse(w, traceID)
		SetSessionIDInResponse(w, sessionID)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GetTraceIDFromRequest extracts Trace-ID from headers, falling back to GCP standard.
func GetTraceIDFromRequest(r *http.Request) string {
	id := r.Header.Get(HeaderTraceID)
	if id == "" {
		id = r.Header.Get(HeaderGCPTrace)
	}
	return id
}

// SetTraceIDInResponse sets the Trace-ID header in the response.
func SetTraceIDInResponse(w http.ResponseWriter, id string) {
	w.Header().Set(HeaderTraceID, id)
}

// GetSessionIDFromRequest extracts Session-ID from headers or cookies.
func GetSessionIDFromRequest(r *http.Request) string {
	id := r.Header.Get(HeaderSessionID)
	if id == "" {
		if cookie, err := r.Cookie(CookieSessionID); err == nil {
			id = cookie.Value
		}
	}
	return id
}

// SetSessionIDInResponse sets the Session-ID header in the response.
func SetSessionIDInResponse(w http.ResponseWriter, id string) {
	w.Header().Set(HeaderSessionID, id)
}

// SetSessionCookie sets the persistent session cookie.
func SetSessionCookie(w http.ResponseWriter, id string) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieSessionID,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400 * 7, // 7 days
	})
}

// GetTraceID returns the trace ID from the context if present.
func GetTraceID(ctx context.Context) string {
	val, _ := ctx.Value(traceContextKey).(string)
	return val
}

// GetSessionID returns the session ID from the context if present.
func GetSessionID(ctx context.Context) string {
	val, _ := ctx.Value(sessionContextKey).(string)
	return val
}

// GetClientIPFromContext returns the client IP address from context if present.
func GetClientIPFromContext(ctx context.Context) string {
	val, _ := ctx.Value(clientIPContextKey).(string)
	return val
}

// GetUserAgentFromContext returns the client user agent from context if present.
func GetUserAgentFromContext(ctx context.Context) string {
	val, _ := ctx.Value(userAgentContextKey).(string)
	return val
}

// WithClientIP injects client IP into context.
func WithClientIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, clientIPContextKey, ip)
}

// WithUserAgent injects user agent into context.
func WithUserAgent(ctx context.Context, ua string) context.Context {
	return context.WithValue(ctx, userAgentContextKey, ua)
}

// GetUserIDFromContext returns user ID from context if present.
func GetUserIDFromContext(ctx context.Context) string {
	val, _ := ctx.Value(userIDContextKey).(string)
	return val
}

// GetEmailFromContext returns user email from context if present.
func GetEmailFromContext(ctx context.Context) string {
	val, _ := ctx.Value(emailContextKey).(string)
	return val
}

// GetUserNameFromContext returns user name from context if present.
func GetUserNameFromContext(ctx context.Context) string {
	val, _ := ctx.Value(userNameContextKey).(string)
	return val
}

// GetOIDCIssuerFromContext returns OIDC issuer from context if present.
func GetOIDCIssuerFromContext(ctx context.Context) string {
	val, _ := ctx.Value(oidcIssuerContextKey).(string)
	return val
}

// GetOIDCSubFromContext returns OIDC sub from context if present.
func GetOIDCSubFromContext(ctx context.Context) string {
	val, _ := ctx.Value(oidcSubContextKey).(string)
	return val
}

// WithUser injects user identity into the context for automatic logging.
func WithUser(ctx context.Context, userID, email string) context.Context {
	ctx = context.WithValue(ctx, userIDContextKey, userID)
	ctx = context.WithValue(ctx, emailContextKey, email)
	return ctx
}

// WithUserMetadata injects full user metadata into context.
func WithUserMetadata(ctx context.Context, userID, email, name, issuer, oidcSub string) context.Context {
	ctx = context.WithValue(ctx, userIDContextKey, userID)
	ctx = context.WithValue(ctx, emailContextKey, email)
	ctx = context.WithValue(ctx, userNameContextKey, name)
	ctx = context.WithValue(ctx, oidcIssuerContextKey, issuer)
	ctx = context.WithValue(ctx, oidcSubContextKey, oidcSub)
	return ctx
}
