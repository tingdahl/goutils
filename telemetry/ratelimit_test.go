package telemetry

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/tingdahl/goutils/config"
	"golang.org/x/time/rate"
)

func TestIPRateLimiter_AllowsAndLimits(t *testing.T) {
	// 2 requests per second, burst 2
	limiter := NewIPRateLimiter(rate.Limit(2), 2)
	defer limiter.Stop()

	handler := limiter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))

	// 1st request should pass
	req1 := httptest.NewRequest("GET", "/test", nil)
	req1.RemoteAddr = "192.0.2.1:1234"
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK on first request, got %d", w1.Code)
	}

	// 2nd request should pass (within burst)
	req2 := httptest.NewRequest("GET", "/test", nil)
	req2.RemoteAddr = "192.0.2.1:1234"
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK on second request, got %d", w2.Code)
	}

	// 3rd request should be blocked (429)
	req3 := httptest.NewRequest("GET", "/test", nil)
	req3.RemoteAddr = "192.0.2.1:1234"
	w3 := httptest.NewRecorder()
	handler.ServeHTTP(w3, req3)
	if w3.Code != http.StatusTooManyRequests {
		t.Fatalf("Expected 429 Too Many Requests on third request, got %d", w3.Code)
	}
	if w3.Header().Get("Retry-After") != "1" {
		t.Errorf("Expected Retry-After: 1, got %q", w3.Header().Get("Retry-After"))
	}

	// Different IP should still pass
	reqDiffIP := httptest.NewRequest("GET", "/test", nil)
	reqDiffIP.RemoteAddr = "192.0.2.2:1234"
	wDiff := httptest.NewRecorder()
	handler.ServeHTTP(wDiff, reqDiffIP)
	if wDiff.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for different IP, got %d", wDiff.Code)
	}
}

func TestGetClientIP_Headers(t *testing.T) {
	// 1. Rightmost X-Forwarded-For should be used to prevent client IP spoofing
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.195, 70.41.3.18")
	req.RemoteAddr = "127.0.0.1:8080"

	ip := GetClientIP(req)
	if ip != "70.41.3.18" {
		t.Errorf("Expected 70.41.3.18, got %s", ip)
	}

	// 2. X-Real-IP takes precedence if provided by reverse proxy
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.Header.Set("X-Real-IP", "198.51.100.22")
	req2.Header.Set("X-Forwarded-For", "203.0.113.195, 70.41.3.18")
	req2.RemoteAddr = "127.0.0.1:8080"
	ip2 := GetClientIP(req2)
	if ip2 != "198.51.100.22" {
		t.Errorf("Expected 198.51.100.22, got %s", ip2)
	}

	// 3. Spoofed headers from untrusted direct peer should be ignored
	reqUntrusted := httptest.NewRequest("GET", "/", nil)
	reqUntrusted.Header.Set("X-Real-IP", "198.51.100.99")
	reqUntrusted.Header.Set("X-Forwarded-For", "198.51.100.99")
	reqUntrusted.RemoteAddr = "203.0.113.50:54321" // Public IP, not loopback or private
	ipUntrusted := GetClientIP(reqUntrusted)
	if ipUntrusted != "203.0.113.50" {
		t.Errorf("Expected untrusted remote peer 203.0.113.50, got %s", ipUntrusted)
	}
}

func TestIsTrustedProxy_ConfiguredEnv(t *testing.T) {
	os.Setenv(config.EnvTrustedProxies, "198.51.100.1, 10.200.0.0/16")
	defer os.Unsetenv(config.EnvTrustedProxies)

	if !IsTrustedProxy("198.51.100.1") {
		t.Error("expected 198.51.100.1 to be trusted")
	}
	if !IsTrustedProxy("10.200.1.5") {
		t.Error("expected 10.200.1.5 to be trusted in CIDR")
	}
	if IsTrustedProxy("198.51.100.2") {
		t.Error("expected 198.51.100.2 to NOT be trusted")
	}
}

func TestIsTrustedProxy_InvalidIP(t *testing.T) {
	if IsTrustedProxy("not-an-ip") {
		t.Error("expected invalid IP string to not be trusted")
	}
}
