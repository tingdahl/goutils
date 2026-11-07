package telemetry_test

import (
	"bytes"
	"context"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tingdahl/goutils/telemetry"
)

type logCaptureHandler struct {
	mu      sync.Mutex
	records []slog.Record
	level   slog.Level
}

func (h *logCaptureHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level
}

func (h *logCaptureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	return nil
}

func (h *logCaptureHandler) WithAttrs(_ []slog.Attr) slog.Handler {
	return h
}

func (h *logCaptureHandler) WithGroup(_ string) slog.Handler {
	return h
}

func (h *logCaptureHandler) findRecord(path string) (slog.Record, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		var found bool
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "path" && a.Value.String() == path {
				found = true
				return false
			}
			return true
		})
		if found {
			return r, true
		}
	}
	return slog.Record{}, false
}

func (h *logCaptureHandler) getAttr(r slog.Record, key string) (slog.Value, bool) {
	var val slog.Value
	var found bool
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			val = a.Value
			found = true
			return false
		}
		return true
	})
	return val, found
}

func TestLoggingMiddleware_ZeroPIILogging(t *testing.T) {
	handler := &logCaptureHandler{level: slog.LevelDebug}
	oldDefault := slog.Default()
	slog.SetDefault(slog.New(handler))
	defer slog.SetDefault(oldDefault)

	var logBuf bytes.Buffer
	oldLogFlags := log.Flags()
	oldLogOutput := log.Writer()
	log.SetFlags(0)
	log.SetOutput(&logBuf)
	defer func() {
		log.SetFlags(oldLogFlags)
		log.SetOutput(oldLogOutput)
	}()

	loggingMw := telemetry.LoggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	rawIP := "198.51.100.77"
	req := httptest.NewRequest(http.MethodPost, "/api/rooms", strings.NewReader(`{}`))
	req.RemoteAddr = rawIP + ":54321"
	req.Header.Set("CF-Connecting-IP", rawIP)
	req.Header.Set("X-Forwarded-For", rawIP+", 10.0.0.1")
	req.Header.Set("X-Session-ID", "test-session-uuid-123")
	req.Header.Set("CF-IPCountry", "SE")
	req.Header.Set("CF-IPCity", "Stockholm")
	req.Header.Set("User-Agent", "TestClient/2.0")
	rec := httptest.NewRecorder()

	loggingMw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	logged := logBuf.String()
	// Verify [REQ] format
	if !strings.Contains(logged, "[REQ] session=test-session-uuid-123 geo=SE/Stockholm method=POST path=/api/rooms status=200") {
		t.Errorf("expected [REQ] session=test-session-uuid-123 geo=SE/Stockholm ..., got: %s", logged)
	}

	// Verify no raw IP leak in stdout/stderr
	if strings.Contains(logged, rawIP) {
		t.Errorf("PII leak detected: raw IP %q found in log: %s", rawIP, logged)
	}

	// Verify structured slog record
	recLog, found := handler.findRecord("/api/rooms")
	if !found {
		t.Fatalf("expected slog record for /api/rooms")
	}

	sessVal, ok := handler.getAttr(recLog, "session_id")
	if !ok || sessVal.String() != "test-session-uuid-123" {
		t.Errorf("expected session_id 'test-session-uuid-123', got %v", sessVal)
	}

	geoVal, ok := handler.getAttr(recLog, "geo")
	if !ok || geoVal.String() != "SE/Stockholm" {
		t.Errorf("expected geo 'SE/Stockholm', got %v", geoVal)
	}

	uaVal, ok := handler.getAttr(recLog, "user_agent")
	if !ok || uaVal.String() != "TestClient/2.0" {
		t.Errorf("expected user_agent 'TestClient/2.0', got %v", uaVal)
	}

	if _, hasRemoteAddr := handler.getAttr(recLog, "remote_addr"); hasRemoteAddr {
		t.Errorf("PII leak: 'remote_addr' found in slog attributes")
	}
	if _, hasClientIP := handler.getAttr(recLog, "client_ip"); hasClientIP {
		t.Errorf("PII leak: 'client_ip' found in slog attributes")
	}
}

func TestLoggingMiddleware_FallbackDefaults(t *testing.T) {
	var logBuf bytes.Buffer
	oldLogFlags := log.Flags()
	oldLogOutput := log.Writer()
	log.SetFlags(0)
	log.SetOutput(&logBuf)
	defer func() {
		log.SetFlags(oldLogFlags)
		log.SetOutput(oldLogOutput)
	}()

	loggingMw := telemetry.LoggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	loggingMw.ServeHTTP(rec, req)

	logged := logBuf.String()
	if !strings.Contains(logged, "[REQ] session=none geo=-/- method=GET path=/test status=200") {
		t.Errorf("expected session=none geo=-/-, got: %s", logged)
	}
}

func TestLoggingMiddleware_PanicRecovery(t *testing.T) {
	loggingMw := telemetry.LoggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("database connection blew up")
	}))

	req := httptest.NewRequest(http.MethodGet, "/crash", nil)
	rec := httptest.NewRecorder()

	loggingMw.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestLoggingMiddleware_ActiveRequests(t *testing.T) {
	var counter atomic.Int64
	loggingMw := telemetry.LoggingMiddlewareWithConfig(telemetry.LoggingConfig{
		ActiveRequests: &counter,
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cur := counter.Load(); cur != 1 {
			t.Errorf("expected activeRequests=1 during execution, got %d", cur)
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/active", nil)
	rec := httptest.NewRecorder()

	loggingMw.ServeHTTP(rec, req)

	if cur := counter.Load(); cur != 0 {
		t.Errorf("expected activeRequests=0 after completion, got %d", cur)
	}
}

type mockFlusherRecorder struct {
	*httptest.ResponseRecorder
	flushed bool
}

func (m *mockFlusherRecorder) Flush() {
	m.flushed = true
}

func TestResponseWriterWrapper_Flusher(t *testing.T) {
	base := &mockFlusherRecorder{ResponseRecorder: httptest.NewRecorder()}
	wrapper := telemetry.NewResponseWriterWrapper(base)

	flusher, ok := any(wrapper).(http.Flusher)
	if !ok {
		t.Fatal("expected ResponseWriterWrapper to implement http.Flusher")
	}

	flusher.Flush()
	if !base.flushed {
		t.Error("expected underlying Flush to be called")
	}
}
