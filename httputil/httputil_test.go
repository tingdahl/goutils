package httputil

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tingdahl/goutils/config"
	"github.com/tingdahl/goutils/storage"
)

func TestGetPortAndHeaders(t *testing.T) {
	// GetPort
	os.Setenv(config.EnvPort, "9999")
	defer os.Unsetenv(config.EnvPort)
	if GetPort() != "9999" {
		t.Errorf("GetPort() = %q, want '9999'", GetPort())
	}

	// SetJSONHeader
	rec := httptest.NewRecorder()
	SetJSONHeader(rec)
	if rec.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", rec.Header().Get("Content-Type"))
	}

	// SetCacheControl
	SetCacheControl(rec, "no-store")
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}

	// RestrictiveRobotsTxtHandler
	handler := RestrictiveRobotsTxtHandler()
	req := httptest.NewRequest("GET", "/robots.txt", nil)
	recRobots := httptest.NewRecorder()
	handler.ServeHTTP(recRobots, req)

	if recRobots.Code != http.StatusOK {
		t.Errorf("robots.txt code = %d, want 200", recRobots.Code)
	}
	if !strings.Contains(recRobots.Body.String(), "Disallow: /") {
		t.Errorf("robots.txt body = %q", recRobots.Body.String())
	}

	// Non-GET method
	reqPost := httptest.NewRequest("POST", "/robots.txt", nil)
	recPost := httptest.NewRecorder()
	handler.ServeHTTP(recPost, reqPost)
	if recPost.Code != http.StatusMethodNotAllowed {
		t.Errorf("robots.txt POST code = %d, want 405", recPost.Code)
	}
}

func TestCORSMiddleware(t *testing.T) {
	opts := CORSOptions{
		AllowedOrigins:   []string{"https://app.example.com"},
		AllowedMethods:   []string{"GET", "POST"},
		AllowedHeaders:   []string{"Content-Type", "Authorization"},
		AllowCredentials: true,
	}
	cors := CORSMiddleware(opts)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	handler := cors(inner)

	// Allowed origin GET
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("Origin", "https://app.example.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "https://app.example.com" {
		t.Errorf("CORS Allow-Origin = %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
	if rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Errorf("CORS Allow-Credentials = %q", rec.Header().Get("Access-Control-Allow-Credentials"))
	}

	// Preflight OPTIONS for allowed origin
	reqOptions := httptest.NewRequest("OPTIONS", "/api/test", nil)
	reqOptions.Header.Set("Origin", "https://app.example.com")
	recOptions := httptest.NewRecorder()
	handler.ServeHTTP(recOptions, reqOptions)

	if recOptions.Code != http.StatusNoContent {
		t.Errorf("OPTIONS code = %d, want 204", recOptions.Code)
	}

	// Preflight OPTIONS for forbidden origin
	reqBadOptions := httptest.NewRequest("OPTIONS", "/api/test", nil)
	reqBadOptions.Header.Set("Origin", "https://evil.com")
	recBadOptions := httptest.NewRecorder()
	handler.ServeHTTP(recBadOptions, reqBadOptions)

	if recBadOptions.Code != http.StatusForbidden {
		t.Errorf("Bad OPTIONS code = %d, want 403", recBadOptions.Code)
	}

	// Wildcard options
	corsWildcard := CORSMiddleware(CORSOptions{AllowAllOrigins: true})(inner)
	reqWild := httptest.NewRequest("GET", "/api/test", nil)
	reqWild.Header.Set("Origin", "https://anywhere.com")
	recWild := httptest.NewRecorder()
	corsWildcard.ServeHTTP(recWild, reqWild)

	if recWild.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("Wildcard Allow-Origin = %q, want '*'", recWild.Header().Get("Access-Control-Allow-Origin"))
	}
	if recWild.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Errorf("Wildcard Allow-Credentials should be empty")
	}
}

func TestSecurityHeadersMiddleware(t *testing.T) {
	cfg := SecurityHeadersConfig{
		CSP:               "default-src 'self'",
		PermissionsPolicy: "geolocation=()",
		HSTS:              true,
		HSTSMaxAge:        31536000,
	}
	mw := SecurityHeadersMiddleware(cfg)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := mw(inner)

	// Plain HTTP - no HSTS
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q", rec.Header().Get("X-Content-Type-Options"))
	}
	if rec.Header().Get("X-Frame-Options") != "SAMEORIGIN" {
		t.Errorf("X-Frame-Options = %q", rec.Header().Get("X-Frame-Options"))
	}
	if rec.Header().Get("Content-Security-Policy") != "default-src 'self'" {
		t.Errorf("CSP = %q", rec.Header().Get("Content-Security-Policy"))
	}
	if rec.Header().Get("Strict-Transport-Security") != "" {
		t.Error("HSTS should not be set over plain HTTP")
	}

	// HTTPS via TLS
	reqTLS := httptest.NewRequest("GET", "https://example.com/", nil)
	reqTLS.TLS = &tls.ConnectionState{}
	recTLS := httptest.NewRecorder()
	handler.ServeHTTP(recTLS, reqTLS)

	if recTLS.Header().Get("Strict-Transport-Security") != "max-age=31536000; includeSubDomains; preload" {
		t.Errorf("HSTS over TLS = %q", recTLS.Header().Get("Strict-Transport-Security"))
	}

	// HTTPS via X-Forwarded-Proto
	reqProxy := httptest.NewRequest("GET", "/", nil)
	reqProxy.Header.Set("X-Forwarded-Proto", "https")
	recProxy := httptest.NewRecorder()
	handler.ServeHTTP(recProxy, reqProxy)

	if recProxy.Header().Get("Strict-Transport-Security") != "max-age=31536000; includeSubDomains; preload" {
		t.Errorf("HSTS over X-Forwarded-Proto = %q", recProxy.Header().Get("Strict-Transport-Security"))
	}
}

func TestStaticServer(t *testing.T) {
	tempDir := t.TempDir()

	// Prepare test files
	_ = os.WriteFile(filepath.Join(tempDir, "index.html"), []byte("<html>Home</html>"), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "app.js"), []byte("console.log('app')"), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "about.html"), []byte("<html>About</html>"), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "secret.env"), []byte("SECRET=123"), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, ".hidden"), []byte("hidden"), 0644)

	// Precompressed brotli file: style.0123456789abcdef01234567.css.br
	hashedCSSName := "style.0123456789abcdef01234567.css"
	cssContent := []byte("body { color: red; }")
	compressedCSS, err := storage.CompressBrotli(cssContent)
	if err != nil {
		t.Fatalf("failed to compress CSS: %v", err)
	}
	_ = os.WriteFile(filepath.Join(tempDir, hashedCSSName+".br"), compressedCSS, 0644)

	server := NewStaticServer(StaticServerConfig{
		PublicDir:     tempDir,
		EnableBrotli:  true,
		FallbackToSPA: true,
	})

	// 1. Root index.html
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Home") {
		t.Errorf("Root / failed: code=%d, body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-cache, must-revalidate" {
		t.Errorf("HTML Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}

	// 2. Direct static file: app.js
	reqJS := httptest.NewRequest("GET", "/app.js", nil)
	recJS := httptest.NewRecorder()
	server.ServeHTTP(recJS, reqJS)
	if recJS.Code != http.StatusOK || !strings.Contains(recJS.Body.String(), "console.log") {
		t.Errorf("app.js failed: code=%d", recJS.Code)
	}
	if !strings.Contains(recJS.Header().Get("Content-Type"), "application/javascript") {
		t.Errorf("app.js Content-Type = %q", recJS.Header().Get("Content-Type"))
	}

	// 3. Clean URL extensionless matching: /about -> about.html
	reqAbout := httptest.NewRequest("GET", "/about", nil)
	recAbout := httptest.NewRecorder()
	server.ServeHTTP(recAbout, reqAbout)
	if recAbout.Code != http.StatusOK || !strings.Contains(recAbout.Body.String(), "About") {
		t.Errorf("/about failed: code=%d, body=%s", recAbout.Code, recAbout.Body.String())
	}

	// 4. SPA fallback for unknown extensionless route: /dashboard -> index.html
	reqDash := httptest.NewRequest("GET", "/dashboard", nil)
	recDash := httptest.NewRecorder()
	server.ServeHTTP(recDash, reqDash)
	if recDash.Code != http.StatusOK || !strings.Contains(recDash.Body.String(), "Home") {
		t.Errorf("SPA fallback failed: code=%d, body=%s", recDash.Code, recDash.Body.String())
	}

	// 5. Brotli serving when client supports it
	reqBr := httptest.NewRequest("GET", "/"+hashedCSSName, nil)
	reqBr.Header.Set("Accept-Encoding", "gzip, br")
	recBr := httptest.NewRecorder()
	server.ServeHTTP(recBr, reqBr)
	if recBr.Code != http.StatusOK {
		t.Fatalf("Brotli request failed: code=%d", recBr.Code)
	}
	if recBr.Header().Get("Content-Encoding") != "br" {
		t.Errorf("Content-Encoding = %q, want 'br'", recBr.Header().Get("Content-Encoding"))
	}
	if recBr.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Errorf("Hashed asset Cache-Control = %q", recBr.Header().Get("Cache-Control"))
	}

	// 6. On-the-fly decompression when client does NOT support Brotli
	reqNoBr := httptest.NewRequest("GET", "/"+hashedCSSName, nil)
	recNoBr := httptest.NewRecorder()
	server.ServeHTTP(recNoBr, reqNoBr)
	if recNoBr.Code != http.StatusOK {
		t.Fatalf("Non-brotli request failed: code=%d", recNoBr.Code)
	}
	if recNoBr.Header().Get("Content-Encoding") != "" {
		t.Errorf("Content-Encoding should be empty, got %q", recNoBr.Header().Get("Content-Encoding"))
	}
	if string(recNoBr.Body.Bytes()) != string(cssContent) {
		t.Errorf("Decompressed content mismatch: got %q, want %q", recNoBr.Body.String(), string(cssContent))
	}

	// 7. Security: Blocked extension (.env)
	reqEnv := httptest.NewRequest("GET", "/secret.env", nil)
	recEnv := httptest.NewRecorder()
	server.ServeHTTP(recEnv, reqEnv)
	if recEnv.Code != http.StatusNotFound {
		t.Errorf("Blocked extension code = %d, want 404", recEnv.Code)
	}

	// 8. Security: Hidden dotfile (/.hidden)
	reqDot := httptest.NewRequest("GET", "/.hidden", nil)
	recDot := httptest.NewRecorder()
	server.ServeHTTP(recDot, reqDot)
	if recDot.Code != http.StatusNotFound {
		t.Errorf("Dotfile code = %d, want 404", recDot.Code)
	}

	// 9. Security: Null byte
	reqNull, _ := http.NewRequest("GET", "/", nil)
	reqNull.URL.Path = "/test\x00.js"
	recNull := httptest.NewRecorder()
	server.ServeHTTP(recNull, reqNull)
	if recNull.Code != http.StatusBadRequest {
		t.Errorf("Null byte code = %d, want 400", recNull.Code)
	}

	// 10. Security: Method not allowed
	reqPost := httptest.NewRequest("POST", "/", nil)
	recPost := httptest.NewRecorder()
	server.ServeHTTP(recPost, reqPost)
	if recPost.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST code = %d, want 405", recPost.Code)
	}

	// 11. No SPA fallback when FallbackToSPA is false
	noSPAServer := NewStaticServer(StaticServerConfig{
		PublicDir:     tempDir,
		FallbackToSPA: false,
	})
	reqNotFound := httptest.NewRequest("GET", "/missing.js", nil)
	recNotFound := httptest.NewRecorder()
	noSPAServer.ServeHTTP(recNotFound, reqNotFound)
	if recNotFound.Code != http.StatusNotFound {
		t.Errorf("No SPA fallback code = %d, want 404", recNotFound.Code)
	}
}
