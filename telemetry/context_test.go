package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTraceMiddleware_GeneratesNewIDs(t *testing.T) {
	var capturedTrace, capturedSession, capturedIP, capturedUA string
	handler := TraceMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedTrace = GetTraceID(r.Context())
		capturedSession = GetSessionID(r.Context())
		capturedIP = GetClientIPFromContext(r.Context())
		capturedUA = GetUserAgentFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("User-Agent", "TestAgent/1.0")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if capturedTrace == "" {
		t.Error("expected non-empty trace ID in context")
	}
	if capturedSession == "" {
		t.Error("expected non-empty session ID in context")
	}
	if capturedUA != "TestAgent/1.0" {
		t.Errorf("capturedUA = %q, want TestAgent/1.0", capturedUA)
	}
	if capturedIP == "" {
		t.Error("expected non-empty client IP in context")
	}

	// Response headers
	if rec.Header().Get(HeaderTraceID) != capturedTrace {
		t.Errorf("Trace-ID header = %q, want %q", rec.Header().Get(HeaderTraceID), capturedTrace)
	}
	if rec.Header().Get(HeaderSessionID) != capturedSession {
		t.Errorf("X-Session-ID header = %q, want %q", rec.Header().Get(HeaderSessionID), capturedSession)
	}

	// Cookie
	cookies := rec.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == CookieSessionID {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected session cookie to be set")
	}
	if sessionCookie.Value != capturedSession {
		t.Errorf("session cookie value = %q, want %q", sessionCookie.Value, capturedSession)
	}
}

func TestTraceMiddleware_PreservesExistingIDs(t *testing.T) {
	customTrace := "my-custom-trace-123"
	customSession := "my-custom-session-456"

	var capturedTrace, capturedSession string
	handler := TraceMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedTrace = GetTraceID(r.Context())
		capturedSession = GetSessionID(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set(HeaderTraceID, customTrace)
	req.Header.Set(HeaderSessionID, customSession)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if capturedTrace != customTrace {
		t.Errorf("capturedTrace = %q, want %q", capturedTrace, customTrace)
	}
	if capturedSession != customSession {
		t.Errorf("capturedSession = %q, want %q", capturedSession, customSession)
	}
}

func TestTraceMiddleware_GCPTraceFallback(t *testing.T) {
	gcpTrace := "105445aa7843bc8bf206b120001000/1;o=1"

	var capturedTrace string
	handler := TraceMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedTrace = GetTraceID(r.Context())
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set(HeaderGCPTrace, gcpTrace)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if capturedTrace != gcpTrace {
		t.Errorf("capturedTrace = %q, want %q", capturedTrace, gcpTrace)
	}
}

func TestContextHandler_InjectsAttributes(t *testing.T) {
	var buf bytes.Buffer
	baseHandler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	contextHandler := &ContextHandler{Next: baseHandler}
	logger := slog.New(contextHandler)

	ctx := context.Background()
	ctx = context.WithValue(ctx, traceContextKey, "trace-abc")
	ctx = context.WithValue(ctx, sessionContextKey, "sess-def")
	ctx = context.WithValue(ctx, clientIPContextKey, "1.2.3.4")
	ctx = context.WithValue(ctx, userAgentContextKey, "TestBrowser/1.0")
	ctx = WithUserMetadata(ctx, "user-42", "user@example.com", "Alice Smith", "https://accounts.google.com", "sub-12345")

	logger.InfoContext(ctx, "hello world")

	var logEntry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &logEntry); err != nil {
		t.Fatalf("failed to parse log output: %v, raw: %s", err, buf.String())
	}

	if logEntry[LogKeyTraceID] != "trace-abc" {
		t.Errorf("log trace_id = %v, want trace-abc", logEntry[LogKeyTraceID])
	}
	if logEntry[LogKeySessionID] != "sess-def" {
		t.Errorf("log session_id = %v, want sess-def", logEntry[LogKeySessionID])
	}
	if logEntry[LogKeyUserID] != "user-42" {
		t.Errorf("log user_id = %v, want user-42", logEntry[LogKeyUserID])
	}
	if logEntry[LogKeyEmail] != "user@example.com" {
		t.Errorf("log email = %v, want user@example.com", logEntry[LogKeyEmail])
	}
	if logEntry[LogKeyClientIP] != "1.2.3.4" {
		t.Errorf("log client_ip = %v, want 1.2.3.4", logEntry[LogKeyClientIP])
	}
	if logEntry[LogKeyUserAgent] != "TestBrowser/1.0" {
		t.Errorf("log user_agent = %v, want TestBrowser/1.0", logEntry[LogKeyUserAgent])
	}
}

func TestContextGettersAndSetters(t *testing.T) {
	ctx := context.Background()
	ctx = WithClientIP(ctx, "10.0.0.1")
	ctx = WithUserAgent(ctx, "CustomUA")
	ctx = WithUser(ctx, "u1", "u1@example.com")

	if GetClientIPFromContext(ctx) != "10.0.0.1" {
		t.Errorf("GetClientIPFromContext = %q, want 10.0.0.1", GetClientIPFromContext(ctx))
	}
	if GetUserAgentFromContext(ctx) != "CustomUA" {
		t.Errorf("GetUserAgentFromContext = %q, want CustomUA", GetUserAgentFromContext(ctx))
	}
	if GetUserIDFromContext(ctx) != "u1" {
		t.Errorf("GetUserIDFromContext = %q, want u1", GetUserIDFromContext(ctx))
	}
	if GetEmailFromContext(ctx) != "u1@example.com" {
		t.Errorf("GetEmailFromContext = %q, want u1@example.com", GetEmailFromContext(ctx))
	}

	ctxMeta := WithUserMetadata(context.Background(), "u2", "u2@example.com", "Bob", "issuer-xyz", "sub-xyz")
	if GetUserNameFromContext(ctxMeta) != "Bob" {
		t.Errorf("GetUserNameFromContext = %q, want Bob", GetUserNameFromContext(ctxMeta))
	}
	if GetOIDCIssuerFromContext(ctxMeta) != "issuer-xyz" {
		t.Errorf("GetOIDCIssuerFromContext = %q, want issuer-xyz", GetOIDCIssuerFromContext(ctxMeta))
	}
	if GetOIDCSubFromContext(ctxMeta) != "sub-xyz" {
		t.Errorf("GetOIDCSubFromContext = %q, want sub-xyz", GetOIDCSubFromContext(ctxMeta))
	}
}

func TestContextHandler_Delegation(t *testing.T) {
	var buf bytes.Buffer
	baseHandler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	contextHandler := &ContextHandler{Next: baseHandler}

	// Enabled check
	if contextHandler.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("expected Enabled(Info) to be false for Warn level handler")
	}
	if !contextHandler.Enabled(context.Background(), slog.LevelWarn) {
		t.Error("expected Enabled(Warn) to be true for Warn level handler")
	}

	// WithAttrs and WithGroup
	hAttrs := contextHandler.WithAttrs([]slog.Attr{slog.String("app", "test")})
	if hAttrs == nil {
		t.Error("WithAttrs returned nil")
	}
	hGroup := contextHandler.WithGroup("mygroup")
	if hGroup == nil {
		t.Error("WithGroup returned nil")
	}
}
