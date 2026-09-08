package telemetry

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tingdahl/goutils/config"
	"golang.org/x/time/rate"
)

type clientVisitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// IPRateLimiter maintains per-IP rate limiters and cleans up inactive visitors.
type IPRateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*clientVisitor
	rate     rate.Limit
	burst    int
	stopChan chan struct{}
}

// NewIPRateLimiter creates a new rate limiter with the specified rate (events/sec) and burst.
func NewIPRateLimiter(r rate.Limit, b int) *IPRateLimiter {
	limiter := &IPRateLimiter{
		visitors: make(map[string]*clientVisitor),
		rate:     r,
		burst:    b,
		stopChan: make(chan struct{}),
	}

	// Periodically remove visitors not seen in the last 5 minutes
	go limiter.cleanupStaleVisitors(5 * time.Minute)

	return limiter
}

// Stop stops the cleanup goroutine for the rate limiter.
func (i *IPRateLimiter) Stop() {
	select {
	case <-i.stopChan:
		// already closed
	default:
		close(i.stopChan)
	}
}

// getVisitor returns or creates the rate limiter for a specific key (IP or user).
func (i *IPRateLimiter) getVisitor(key string) *rate.Limiter {
	i.mu.Lock()
	defer i.mu.Unlock()

	v, exists := i.visitors[key]
	if !exists {
		limiter := rate.NewLimiter(i.rate, i.burst)
		i.visitors[key] = &clientVisitor{limiter: limiter, lastSeen: time.Now()}
		return limiter
	}

	v.lastSeen = time.Now()
	return v.limiter
}

func (i *IPRateLimiter) cleanupStaleVisitors(maxIdle time.Duration) {
	ticker := time.NewTicker(maxIdle)
	defer ticker.Stop()
	for {
		select {
		case <-i.stopChan:
			return
		case <-ticker.C:
			i.mu.Lock()
			now := time.Now()
			for key, v := range i.visitors {
				if now.Sub(v.lastSeen) > maxIdle {
					delete(i.visitors, key)
				}
			}
			i.mu.Unlock()
		}
	}
}

// IsTrustedProxy checks whether the direct peer IP address is a trusted proxy or private/loopback network.
func IsTrustedProxy(remoteIP string) bool {
	parsedIP := net.ParseIP(remoteIP)
	if parsedIP == nil {
		return false
	}

	// Check TRUSTED_PROXIES env var if configured
	if trustedEnv := config.Config().GetTrustedProxies(); trustedEnv != "" {
		for _, entry := range strings.Split(trustedEnv, ",") {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			if strings.Contains(entry, "/") {
				_, cidrNet, err := net.ParseCIDR(entry)
				if err == nil && cidrNet.Contains(parsedIP) {
					return true
				}
			} else if entry == remoteIP {
				return true
			}
		}
		return false
	}

	// By default, trust loopback and RFC 1918 / RFC 4193 private networks
	return parsedIP.IsLoopback() || parsedIP.IsPrivate()
}

// GetClientIP extracts the real client IP address from standard reverse proxy headers or remote address.
// Proxy headers are only trusted if the direct peer is a trusted proxy or private/loopback network.
func GetClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || net.ParseIP(host) == nil {
		host = r.RemoteAddr
	}

	if IsTrustedProxy(host) {
		if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
			ip := strings.TrimSpace(xrip)
			if net.ParseIP(ip) != nil {
				return ip
			}
		}

		// Check Forwarded / X-Forwarded-For headers (scan right-to-left for closest upstream hop)
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			for i := len(parts) - 1; i >= 0; i-- {
				ip := strings.TrimSpace(parts[i])
				if net.ParseIP(ip) != nil {
					return ip
				}
			}
		}
	}

	return host
}

// Middleware returns an HTTP middleware that enforces rate limiting per client IP.
func (i *IPRateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := GetClientIP(r)
		limiter := i.getVisitor(ip)

		if !limiter.Allow() {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}
