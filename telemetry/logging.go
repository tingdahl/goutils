package telemetry

import (
	"bufio"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

const (
	// HeaderCFCountry is Cloudflare's visitor country code header.
	HeaderCFCountry = "CF-IPCountry"
	// HeaderCFCity is Cloudflare's visitor city name header.
	HeaderCFCity = "CF-IPCity"
)

// ResponseWriterWrapper intercepts HTTP status code and bytes written for access logging.
// It transparently preserves http.Flusher and http.Hijacker interfaces when supported by the underlying writer.
type ResponseWriterWrapper struct {
	http.ResponseWriter
	StatusCode   int
	BytesWritten int64
	wroteHeader  bool
}

// NewResponseWriterWrapper creates a ResponseWriterWrapper defaulting to status 200 OK.
func NewResponseWriterWrapper(w http.ResponseWriter) *ResponseWriterWrapper {
	return &ResponseWriterWrapper{
		ResponseWriter: w,
		StatusCode:     http.StatusOK,
	}
}

func (rw *ResponseWriterWrapper) WriteHeader(statusCode int) {
	if !rw.wroteHeader {
		rw.StatusCode = statusCode
		rw.wroteHeader = true
		rw.ResponseWriter.WriteHeader(statusCode)
	}
}

func (rw *ResponseWriterWrapper) Write(b []byte) (int, error) {
	if !rw.wroteHeader {
		rw.WriteHeader(http.StatusOK)
	}
	n, err := rw.ResponseWriter.Write(b)
	rw.BytesWritten += int64(n)
	return n, err
}

func (rw *ResponseWriterWrapper) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}

func (rw *ResponseWriterWrapper) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (rw *ResponseWriterWrapper) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := rw.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// GetVisitorGeo extracts coarse Cloudflare edge geolocation headers (Zero-PII).
// Returns (country, city), falling back to "-" if missing.
func GetVisitorGeo(r *http.Request) (country, city string) {
	country = r.Header.Get(HeaderCFCountry)
	if country == "" {
		country = "-"
	}
	city = r.Header.Get(HeaderCFCity)
	if city == "" {
		city = "-"
	}
	return country, city
}

// ActiveRequestsCounter is an optional interface to track in-flight request concurrency.
type ActiveRequestsCounter interface {
	Add(delta int64) int64
}

// LoggingConfig configures the Zero-PII HTTP logging middleware.
type LoggingConfig struct {
	// ActiveRequests optionally tracks concurrency of active in-flight requests.
	ActiveRequests ActiveRequestsCounter
	// SlowRequestThreshold sets duration after which an in-progress log warning is emitted (default: 10s).
	SlowRequestThreshold time.Duration
	// RecoverPanics captures panics, logs stack traces, and responds with HTTP 500 (default: true).
	RecoverPanics bool
}

func isHealthCheckPath(path string) bool {
	return path == "/api/health" || path == "/health"
}

// LoggingMiddleware creates a standard Zero-PII HTTP logging middleware with default options.
// It extracts ephemeral in-memory session ID and coarse Cloudflare geolocation headers,
// captures panics, warns on slow requests (>10s), and guarantees no raw IP addresses are logged.
func LoggingMiddleware(next http.Handler) http.Handler {
	return LoggingMiddlewareWithConfig(LoggingConfig{
		SlowRequestThreshold: 10 * time.Second,
		RecoverPanics:        true,
	})(next)
}

// LoggingMiddlewareWithConfig creates a Zero-PII HTTP logging middleware with custom configuration.
func LoggingMiddlewareWithConfig(cfg LoggingConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			var active int64
			if cfg.ActiveRequests != nil {
				active = cfg.ActiveRequests.Add(1)
				defer cfg.ActiveRequests.Add(-1)
			}

			ctx := r.Context()

			// 1. Extract In-Memory Session ID from client
			sessionID := r.Header.Get(HeaderSessionID)
			if sessionID == "" {
				sessionID = "none"
			}

			// 2. Extract Cloudflare Coarse Geo (Zero-PII)
			country, city := GetVisitorGeo(r)

			// Log immediately for SSE or streaming connections
			if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
				attrs := []any{
					"method", r.Method,
					"path", r.URL.Path,
					"session_id", sessionID,
					"geo", fmt.Sprintf("%s/%s", country, city),
				}
				if cfg.ActiveRequests != nil {
					attrs = append(attrs, "active_requests", active)
				}
				slog.InfoContext(ctx, "HTTP streaming connection opened", attrs...)
			}

			// Alert on slow requests (>10s)
			threshold := cfg.SlowRequestThreshold
			if threshold <= 0 {
				threshold = 10 * time.Second
			}
			longRunningTimer := time.AfterFunc(threshold, func() {
				slog.InfoContext(ctx, "HTTP request in progress (>10s)",
					"method", r.Method,
					"path", r.URL.Path,
					"session_id", sessionID,
					"elapsed_ms", time.Since(start).Milliseconds(),
				)
			})
			defer longRunningTimer.Stop()

			wrapped := NewResponseWriterWrapper(w)

			defer func() {
				if cfg.RecoverPanics {
					if p := recover(); p != nil {
						wrapped.StatusCode = http.StatusInternalServerError
						slog.ErrorContext(ctx, "HTTP handler panic recovered",
							"error", fmt.Sprintf("%v", p),
							"stack", string(debug.Stack()),
							"method", r.Method,
							"path", r.URL.Path,
							"session_id", sessionID,
						)
						http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
					}
				}

				duration := time.Since(start)
				userAgent := r.UserAgent()

				// Standard Zero-PII access log line
				log.Printf("[REQ] session=%s geo=%s/%s method=%s path=%s status=%d duration=%s",
					sessionID,
					country,
					city,
					r.Method,
					r.URL.Path,
					wrapped.StatusCode,
					duration,
				)

				// Structured slog record
				attrs := []any{
					"session_id", sessionID,
					"geo", fmt.Sprintf("%s/%s", country, city),
					"method", r.Method,
					"path", r.URL.Path,
					"status", wrapped.StatusCode,
					"duration_ms", duration.Milliseconds(),
					"bytes", wrapped.BytesWritten,
				}
				if cfg.ActiveRequests != nil {
					attrs = append(attrs, "active_requests", active)
				}
				if userAgent != "" {
					attrs = append(attrs, "user_agent", userAgent)
				}
				if ctx.Err() != nil {
					attrs = append(attrs, "context_err", ctx.Err().Error())
				}

				switch {
				case wrapped.StatusCode >= http.StatusInternalServerError:
					slog.ErrorContext(ctx, "HTTP request completed with server error", attrs...)
				case wrapped.StatusCode >= http.StatusBadRequest:
					slog.WarnContext(ctx, "HTTP request completed with client error", attrs...)
				case isHealthCheckPath(r.URL.Path):
					slog.DebugContext(ctx, "HTTP health check probe", attrs...)
				case r.Method == http.MethodOptions:
					slog.DebugContext(ctx, "HTTP preflight completed", attrs...)
				default:
					slog.InfoContext(ctx, "HTTP request completed", attrs...)
				}
			}()

			next.ServeHTTP(wrapped, r)
		})
	}
}

